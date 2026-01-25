/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rspcontroller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	nodeutil "k8s.io/component-helpers/node/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

// Reconciler reconciles ReplicatedStoragePool resources.
// It calculates EligibleNodes based on LVMVolumeGroups, Nodes, and agent pod status.
type Reconciler struct {
	cl                client.Client
	log               logr.Logger
	agentPodNamespace string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new RSP reconciler.
// agentPodNamespace is the namespace where agent pods are deployed (used for AgentReady status).
func NewReconciler(cl client.Client, log logr.Logger, agentPodNamespace string) *Reconciler {
	return &Reconciler{
		cl:                cl,
		log:               log,
		agentPodNamespace: agentPodNamespace,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get RSP.
	rsp, err := r.getRSP(rf.Ctx(), req.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return rf.Done().ToCtrl()
		}
		return rf.Fail(err).ToCtrl()
	}

	// Take patch base before mutations.
	base := rsp.DeepCopy()

	// Get LVGs referenced by RSP.
	lvgs, lvgsNotFoundErr, err := r.getLVGsByRSP(rf.Ctx(), rsp)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Cannot calculate eligible nodes if LVGs are missing.
	// Set condition and keep old eligible nodes.
	if lvgsNotFoundErr != nil {
		if applyReadyCondFalse(rsp,
			v1alpha1.ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound,
			fmt.Sprintf("Some LVMVolumeGroups not found: %v", lvgsNotFoundErr),
		) {
			return rf.DoneOrFail(r.patchRSPStatus(rf.Ctx(), rsp, base, false)).ToCtrl()
		}

		return rf.Done().ToCtrl()
	}

	// Validate RSP and LVGs are correctly configured.
	if err := validateRSPAndLVGs(rsp, lvgs); err != nil {
		if applyReadyCondFalse(rsp,
			v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidLVMVolumeGroup,
			fmt.Sprintf("RSP/LVG validation failed: %v", err),
		) {
			return rf.DoneOrFail(r.patchRSPStatus(rf.Ctx(), rsp, base, false)).ToCtrl()
		}

		return rf.Done().ToCtrl()
	}

	nodeSelector := labels.Everything()

	// Validate NodeLabelSelector if present.
	if rsp.Spec.NodeLabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(rsp.Spec.NodeLabelSelector)

		if err != nil {
			if applyReadyCondFalse(rsp,
				v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidNodeLabelSelector,
				fmt.Sprintf("Invalid NodeLabelSelector: %v", err),
			) {
				return rf.DoneOrFail(r.patchRSPStatus(rf.Ctx(), rsp, base, false)).ToCtrl()
			}
			return rf.Done().ToCtrl()
		}

		reqs, _ := selector.Requirements()
		nodeSelector = nodeSelector.Add(reqs...)
	}

	if len(rsp.Spec.Zones) > 0 {
		req, err := labels.NewRequirement(corev1.LabelTopologyZone, selection.In, rsp.Spec.Zones)
		if err != nil {
			if applyReadyCondFalse(rsp,
				v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidNodeLabelSelector,
				fmt.Sprintf("Invalid Zones: %v", err),
			) {
				return rf.DoneOrFail(r.patchRSPStatus(rf.Ctx(), rsp, base, false)).ToCtrl()
			}
			return rf.Done().ToCtrl()
		}
		nodeSelector = nodeSelector.Add(*req)
	}

	// Get all nodes matching selector.
	nodes, err := r.getSortedNodes(rf.Ctx(), nodeSelector)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Get agent readiness per node.
	agentReadyByNode, err := r.getAgentReadiness(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	eligibleNodes, worldStateExpiresAt := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

	// Apply changes to status.
	changed := applyEligibleNodesAndIncrementRevisionIfChanged(rsp, eligibleNodes)

	// Set condition to success.
	changed = applyReadyCondTrue(rsp,
		v1alpha1.ReplicatedStoragePoolCondReadyReasonReady,
		fmt.Sprintf("Eligible nodes calculated successfully: %d nodes", len(eligibleNodes)),
	) || changed

	if changed {
		if err := r.patchRSPStatus(rf.Ctx(), rsp, base, true); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	// Schedule requeue when grace period will expire, even if nothing changed.
	// This ensures nodes beyond grace period will be removed from EligibleNodes.
	if worldStateExpiresAt != nil {
		return rf.RequeueAfter(time.Until(*worldStateExpiresAt)).ToCtrl()
	}

	return rf.Done().ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers: Reconcile (non-I/O)
//

// --- Compute helpers ---

// computeActualEligibleNodes computes the list of eligible nodes for an RSP.
//
// IMPORTANT: The nodes slice must be pre-filtered by the caller (Reconcile) to include
// only nodes matching RSP's NodeLabelSelector and Zones. This function does NOT perform
// zone/label filtering - it assumes all passed nodes are potential candidates.
//
// It also returns worldStateExpiresAt - the earliest time when a node's grace period
// will expire and the eligible nodes list may change. Returns nil if no expiration is needed.
func computeActualEligibleNodes(
	rsp *v1alpha1.ReplicatedStoragePool,
	lvgs map[string]lvgView,
	nodes []nodeView,
	agentReadyByNode map[string]bool,
) (eligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode, worldStateExpiresAt *time.Time) {
	// Build LVG lookup by node name.
	lvgByNode := buildLVGByNodeMap(lvgs, rsp)

	// Get grace period for not-ready nodes from spec.
	gracePeriod := rsp.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration

	result := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0)
	var earliestExpiration *time.Time

	for i := range nodes {
		node := &nodes[i]

		// Check node readiness and grace period.
		nodeReady, notReadyBeyondGrace, graceExpiresAt := isNodeReadyOrWithinGrace(node.ready, gracePeriod)
		if notReadyBeyondGrace {
			// Node has been not-ready beyond grace period - exclude from eligible nodes.
			continue
		}

		// Track earliest grace period expiration for NotReady nodes within grace.
		if !nodeReady && !graceExpiresAt.IsZero() {
			if earliestExpiration == nil || graceExpiresAt.Before(*earliestExpiration) {
				earliestExpiration = &graceExpiresAt
			}
		}

		// Get LVGs for this node (may be empty for client-only/tiebreaker nodes).
		nodeLVGs := lvgByNode[node.name]

		// Build eligible node entry.
		eligibleNode := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        node.name,
			ZoneName:        node.zoneName,
			NodeReady:       nodeReady,
			Unschedulable:   node.unschedulable,
			LVMVolumeGroups: nodeLVGs,
			AgentReady:      agentReadyByNode[node.name],
		}

		result = append(result, eligibleNode)
	}

	// Result is already sorted by node name because nodes are pre-sorted by getSortedNodes.
	return result, earliestExpiration
}

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// nodeView is a lightweight read-only snapshot of Node fields needed for RSP reconciliation.
// It is safe to use with UnsafeDisableDeepCopy because it copies only scalar values.
type nodeView struct {
	name          string
	zoneName      string
	unschedulable bool
	ready         nodeViewReady
}

// nodeViewReady contains Ready condition state needed for grace period calculation.
type nodeViewReady struct {
	status             bool      // True if Ready condition is True
	hasCondition       bool      // True if Ready condition exists
	lastTransitionTime time.Time // For grace period calculation
}

// newNodeView creates a nodeView from a Node.
// The unsafeNode may come from cache without DeepCopy; nodeView copies only the needed scalar fields.
func newNodeView(unsafeNode *corev1.Node) nodeView {
	view := nodeView{
		name:          unsafeNode.Name,
		zoneName:      unsafeNode.Labels[corev1.LabelTopologyZone],
		unschedulable: unsafeNode.Spec.Unschedulable,
	}

	_, readyCond := nodeutil.GetNodeCondition(&unsafeNode.Status, corev1.NodeReady)
	if readyCond != nil {
		view.ready = nodeViewReady{
			hasCondition:       true,
			status:             readyCond.Status == corev1.ConditionTrue,
			lastTransitionTime: readyCond.LastTransitionTime.Time,
		}
	}

	return view
}

// lvgView is a lightweight read-only snapshot of LVMVolumeGroup fields needed for RSP reconciliation.
// It is safe to use with UnsafeDisableDeepCopy because it copies only scalar values and small maps.
type lvgView struct {
	name              string
	nodeName          string
	unschedulable     bool
	ready             bool                // Ready condition status
	specThinPoolNames map[string]struct{} // set of thin pool names from spec
	thinPoolReady     map[string]struct{} // set of ready thin pool names from status
}

// newLVGView creates an lvgView from an LVMVolumeGroup.
// The unsafeLVG may come from cache without DeepCopy; lvgView copies only the needed fields.
func newLVGView(unsafeLVG *snc.LVMVolumeGroup) lvgView {
	view := lvgView{
		name:     unsafeLVG.Name,
		nodeName: unsafeLVG.Spec.Local.NodeName,
		ready:    meta.IsStatusConditionTrue(unsafeLVG.Status.Conditions, "Ready"),
	}

	// Check unschedulable annotation.
	_, view.unschedulable = unsafeLVG.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]

	// Copy spec thin pool names (for validation).
	if len(unsafeLVG.Spec.ThinPools) > 0 {
		view.specThinPoolNames = make(map[string]struct{}, len(unsafeLVG.Spec.ThinPools))
		for _, tp := range unsafeLVG.Spec.ThinPools {
			view.specThinPoolNames[tp.Name] = struct{}{}
		}
	}

	// Copy status thin pool readiness (only ready thin pools).
	if len(unsafeLVG.Status.ThinPools) > 0 {
		view.thinPoolReady = make(map[string]struct{}, len(unsafeLVG.Status.ThinPools))
		for _, tp := range unsafeLVG.Status.ThinPools {
			if tp.Ready {
				view.thinPoolReady[tp.Name] = struct{}{}
			}
		}
	}

	return view
}

// --- Pure helpers ---

// isLVGReady checks if an LVMVolumeGroup is ready.
// For LVM (no thin pool): checks if the LVG Ready condition is True.
// For LVMThin (with thin pool): checks if the LVG Ready condition is True AND
// the specific thin pool status.ready is true.
func isLVGReady(lvg *lvgView, thinPoolName string) bool {
	// Check LVG Ready condition.
	if !lvg.ready {
		return false
	}

	// If no thin pool specified (LVM type), LVG Ready condition is sufficient.
	if thinPoolName == "" {
		return true
	}

	// For LVMThin, also check thin pool readiness.
	_, ready := lvg.thinPoolReady[thinPoolName]
	return ready
}

// isNodeReadyOrWithinGrace checks node readiness and grace period status.
// Returns:
//   - nodeReady: true if node is Ready
//   - notReadyBeyondGrace: true if node is NotReady and beyond grace period (should be excluded)
//   - graceExpiresAt: when the grace period will expire (zero if node is Ready or beyond grace)
func isNodeReadyOrWithinGrace(ready nodeViewReady, gracePeriod time.Duration) (nodeReady bool, notReadyBeyondGrace bool, graceExpiresAt time.Time) {
	if !ready.hasCondition {
		// No Ready condition - consider not ready but within grace (unknown state).
		return false, false, time.Time{}
	}

	if ready.status {
		return true, false, time.Time{}
	}

	// Node is not ready - check grace period.
	graceExpiresAt = ready.lastTransitionTime.Add(gracePeriod)
	if time.Now().After(graceExpiresAt) {
		return false, true, time.Time{} // Beyond grace period.
	}

	return false, false, graceExpiresAt // Within grace period.
}

// --- Comparison helpers ---

// areEligibleNodesEqual compares two eligible nodes slices for equality.
func areEligibleNodesEqual(a, b []v1alpha1.ReplicatedStoragePoolEligibleNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].NodeName != b[i].NodeName ||
			a[i].ZoneName != b[i].ZoneName ||
			a[i].NodeReady != b[i].NodeReady ||
			a[i].Unschedulable != b[i].Unschedulable ||
			a[i].AgentReady != b[i].AgentReady {
			return false
		}
		if !areLVGsEqual(a[i].LVMVolumeGroups, b[i].LVMVolumeGroups) {
			return false
		}
	}
	return true
}

// areLVGsEqual compares two LVG slices for equality.
func areLVGsEqual(a, b []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].ThinPoolName != b[i].ThinPoolName ||
			a[i].Unschedulable != b[i].Unschedulable ||
			a[i].Ready != b[i].Ready {
			return false
		}
	}
	return true
}

// --- Apply helpers ---

// applyReadyCondTrue sets the Ready condition to True.
// Returns true if the condition was changed.
func applyReadyCondTrue(rsp *v1alpha1.ReplicatedStoragePool, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsp, metav1.Condition{
		Type:    v1alpha1.ReplicatedStoragePoolCondReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyReadyCondFalse sets the Ready condition to False.
// Returns true if the condition was changed.
func applyReadyCondFalse(rsp *v1alpha1.ReplicatedStoragePool, reason, message string) bool {
	return objutilv1.SetStatusCondition(rsp, metav1.Condition{
		Type:    v1alpha1.ReplicatedStoragePoolCondReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyEligibleNodesAndIncrementRevisionIfChanged updates eligible nodes in RSP status
// and increments revision if nodes changed. Returns true if changed.
func applyEligibleNodesAndIncrementRevisionIfChanged(
	rsp *v1alpha1.ReplicatedStoragePool,
	eligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode,
) bool {
	if areEligibleNodesEqual(rsp.Status.EligibleNodes, eligibleNodes) {
		return false
	}
	rsp.Status.EligibleNodes = eligibleNodes
	rsp.Status.EligibleNodesRevision++
	return true
}

// --- Validate helpers ---

// validateRSPAndLVGs validates that RSP and LVGs are correctly configured.
// It checks:
//   - For LVMThin type, thinPoolName exists in each referenced LVG's Spec.ThinPools
func validateRSPAndLVGs(rsp *v1alpha1.ReplicatedStoragePool, lvgs map[string]lvgView) error {
	// Validate ThinPool references for LVMThin type.
	if rsp.Spec.Type == v1alpha1.ReplicatedStoragePoolTypeLVMThin {
		for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
			if rspLVG.ThinPoolName == "" {
				return fmt.Errorf("LVMVolumeGroup %q: thinPoolName is required for LVMThin type", rspLVG.Name)
			}

			lvg, ok := lvgs[rspLVG.Name]
			if !ok {
				// LVG not found in the provided map - this is a bug in the calling code.
				panic(fmt.Sprintf("validateRSPAndLVGs: LVG %q not found in lvgs (invariant violation)", rspLVG.Name))
			}

			// Check if ThinPool exists in LVG.
			if _, thinPoolFound := lvg.specThinPoolNames[rspLVG.ThinPoolName]; !thinPoolFound {
				return fmt.Errorf("LVMVolumeGroup %q: thinPool %q not found in Spec.ThinPools", rspLVG.Name, rspLVG.ThinPoolName)
			}
		}
	}

	return nil
}

// --- Construction helpers ---

// buildLVGByNodeMap builds a map of node name to LVG entries for the RSP.
// Iterates over rsp.Spec.LVMVolumeGroups and looks up each LVG in the provided map.
// LVGs are sorted by name per node for deterministic output.
func buildLVGByNodeMap(
	lvgs map[string]lvgView,
	rsp *v1alpha1.ReplicatedStoragePool,
) map[string][]v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	result := make(map[string][]v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup)

	for _, ref := range rsp.Spec.LVMVolumeGroups {
		lvg, ok := lvgs[ref.Name]
		if !ok {
			// LVG not found - skip (caller should have validated).
			continue
		}

		// Get node name from LVG.
		nodeName := lvg.nodeName
		if nodeName == "" {
			continue
		}

		// Determine readiness of the LVG (and thin pool if applicable).
		ready := isLVGReady(&lvg, ref.ThinPoolName)

		entry := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          lvg.name,
			ThinPoolName:  ref.ThinPoolName,
			Unschedulable: lvg.unschedulable,
			Ready:         ready,
		}

		result[nodeName] = append(result[nodeName], entry)
	}

	// Sort LVGs by name for deterministic output.
	for nodeName := range result {
		sort.Slice(result[nodeName], func(i, j int) bool {
			return result[nodeName][i].Name < result[nodeName][j].Name
		})
	}

	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// --- ReplicatedStoragePool (RSP) ---

// getRSP fetches an RSP by name.
func (r *Reconciler) getRSP(ctx context.Context, name string) (*v1alpha1.ReplicatedStoragePool, error) {
	var rsp v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

// patchRSPStatus patches the RSP status subresource.
func (r *Reconciler) patchRSPStatus(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	base *v1alpha1.ReplicatedStoragePool,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, rsp, patch)
}

// --- LVMVolumeGroup (LVG) ---

// getLVGsByRSP fetches LVGs referenced by the given RSP and returns them as a map keyed by LVG name.
// Uses UnsafeDisableDeepCopy for efficiency.
// Returns:
//   - lvgs: map of LVG name to lvgView snapshot for found LVGs
//   - lvgsNotFoundErr: merged error for any NotFound cases (nil if all found)
//   - err: non-NotFound error (if any occurred, lvgs will be nil)
func (r *Reconciler) getLVGsByRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) (
	lvgs map[string]lvgView,
	lvgsNotFoundErr error,
	err error,
) {
	if rsp == nil || len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, nil, nil
	}

	// Build a set of wanted LVG names.
	wantedNames := make(map[string]struct{}, len(rsp.Spec.LVMVolumeGroups))
	for _, ref := range rsp.Spec.LVMVolumeGroups {
		wantedNames[ref.Name] = struct{}{}
	}

	// List all LVGs with UnsafeDisableDeepCopy and filter in-memory.
	var unsafeList snc.LVMVolumeGroupList
	if err := r.cl.List(ctx, &unsafeList, client.UnsafeDisableDeepCopy); err != nil {
		return nil, nil, err
	}

	lvgs = make(map[string]lvgView, len(rsp.Spec.LVMVolumeGroups))

	for i := range unsafeList.Items {
		unsafeLVG := &unsafeList.Items[i]
		if _, wanted := wantedNames[unsafeLVG.Name]; !wanted {
			continue
		}
		lvgs[unsafeLVG.Name] = newLVGView(unsafeLVG)
	}

	// Check for not found LVGs.
	var notFoundErrs []error
	for name := range wantedNames {
		if _, found := lvgs[name]; !found {
			notFoundErrs = append(notFoundErrs, fmt.Errorf("LVMVolumeGroup %q not found", name))
		}
	}

	// Sort notFoundErrs for deterministic error message.
	sort.Slice(notFoundErrs, func(i, j int) bool {
		return notFoundErrs[i].Error() < notFoundErrs[j].Error()
	})

	return lvgs, errors.Join(notFoundErrs...), nil
}

// --- Node ---

// getSortedNodes fetches nodes matching the given selector and returns lightweight nodeView snapshots,
// sorted by name. Uses UnsafeDisableDeepCopy for efficiency.
// The selector should include NodeLabelSelector and Zones requirements from RSP.
func (r *Reconciler) getSortedNodes(ctx context.Context, selector labels.Selector) ([]nodeView, error) {
	var unsafeList corev1.NodeList
	if err := r.cl.List(ctx, &unsafeList,
		client.MatchingLabelsSelector{Selector: selector},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	views := make([]nodeView, len(unsafeList.Items))
	for i := range unsafeList.Items {
		views[i] = newNodeView(&unsafeList.Items[i])
	}

	sort.Slice(views, func(i, j int) bool {
		return views[i].name < views[j].name
	})

	return views, nil
}

// --- Pod ---

// getAgentReadiness fetches agent pods and returns a map of nodeName -> isReady.
// Uses UnsafeDisableDeepCopy for efficiency. Nodes without agent pods are not included
// in the map, which results in AgentReady=false when accessed via map lookup.
func (r *Reconciler) getAgentReadiness(ctx context.Context) (map[string]bool, error) {
	var unsafeList corev1.PodList
	if err := r.cl.List(ctx, &unsafeList,
		client.InNamespace(r.agentPodNamespace),
		client.MatchingLabels{"app": "agent"},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	result := make(map[string]bool, len(unsafeList.Items))
	for i := range unsafeList.Items {
		unsafePod := &unsafeList.Items[i]
		nodeName := unsafePod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		result[nodeName] = isPodReady(unsafePod)
	}
	return result, nil
}
