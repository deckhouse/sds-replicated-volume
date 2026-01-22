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

// --- Wiring / construction ---

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

// --- Reconcile ---

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
	lvgs, lvgsNotFoundErr, err := r.getSortedLVGsByRSP(rf.Ctx(), rsp)
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

	// Get agent pods to determine agent readiness per node.
	agentPods, err := r.getAgentPods(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	agentReadyByNode := computeActualAgentReadiness(agentPods)

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

// =============================================================================
// Helpers: Reconcile (non-I/O)
// =============================================================================

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
	lvgs []snc.LVMVolumeGroup,
	nodes []corev1.Node,
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
		nodeReady, notReadyBeyondGrace, graceExpiresAt := isNodeReadyOrWithinGrace(node, gracePeriod)
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
		nodeLVGs := lvgByNode[node.Name]

		// Build eligible node entry.
		eligibleNode := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        node.Name,
			ZoneName:        node.Labels[corev1.LabelTopologyZone],
			NodeReady:       nodeReady,
			Unschedulable:   node.Spec.Unschedulable,
			LVMVolumeGroups: nodeLVGs,
			AgentReady:      agentReadyByNode[node.Name],
		}

		result = append(result, eligibleNode)
	}

	// Result is already sorted by node name because nodes are pre-sorted by getSortedNodes.
	return result, earliestExpiration
}

// computeActualAgentReadiness computes agent readiness by node from agent pods.
// Returns a map of nodeName -> isReady. Nodes without agent pods are not included
// in the map, which results in AgentReady=false when accessed via map lookup.
func computeActualAgentReadiness(pods []corev1.Pod) map[string]bool {
	result := make(map[string]bool)
	for i := range pods {
		pod := &pods[i]
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		result[nodeName] = isPodReady(pod)
	}
	return result
}

// --- Pure helpers ---

// isLVGReady checks if an LVMVolumeGroup is ready.
// For LVM (no thin pool): checks if the LVG Ready condition is True.
// For LVMThin (with thin pool): checks if the LVG Ready condition is True AND
// the specific thin pool status.ready is true.
func isLVGReady(lvg *snc.LVMVolumeGroup, thinPoolName string) bool {
	// Check LVG Ready condition.
	if !meta.IsStatusConditionTrue(lvg.Status.Conditions, "Ready") {
		return false
	}

	// If no thin pool specified (LVM type), LVG Ready condition is sufficient.
	if thinPoolName == "" {
		return true
	}

	// For LVMThin, also check thin pool readiness.
	for _, tp := range lvg.Status.ThinPools {
		if tp.Name == thinPoolName {
			return tp.Ready
		}
	}

	// Thin pool not found in status - not ready.
	return false
}

// isNodeReadyOrWithinGrace checks node readiness and grace period status.
// Returns:
//   - nodeReady: true if node is Ready
//   - notReadyBeyondGrace: true if node is NotReady and beyond grace period (should be excluded)
//   - graceExpiresAt: when the grace period will expire (zero if node is Ready or beyond grace)
func isNodeReadyOrWithinGrace(node *corev1.Node, gracePeriod time.Duration) (nodeReady bool, notReadyBeyondGrace bool, graceExpiresAt time.Time) {
	_, readyCond := nodeutil.GetNodeCondition(&node.Status, corev1.NodeReady)

	if readyCond == nil {
		// No Ready condition - consider not ready but within grace (unknown state).
		return false, false, time.Time{}
	}

	if readyCond.Status == corev1.ConditionTrue {
		return true, false, time.Time{}
	}

	// Node is not ready - check grace period.
	graceExpiresAt = readyCond.LastTransitionTime.Time.Add(gracePeriod)
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
func validateRSPAndLVGs(rsp *v1alpha1.ReplicatedStoragePool, lvgs []snc.LVMVolumeGroup) error {
	// Build LVG lookup by name.
	lvgByName := make(map[string]*snc.LVMVolumeGroup, len(lvgs))
	for i := range lvgs {
		lvgByName[lvgs[i].Name] = &lvgs[i]
	}

	// Validate ThinPool references for LVMThin type.
	if rsp.Spec.Type == v1alpha1.RSPTypeLVMThin {
		for _, rspLVG := range rsp.Spec.LVMVolumeGroups {
			if rspLVG.ThinPoolName == "" {
				return fmt.Errorf("LVMVolumeGroup %q: thinPoolName is required for LVMThin type", rspLVG.Name)
			}

			lvg, ok := lvgByName[rspLVG.Name]
			if !ok {
				// LVG not found in the provided list - this is a bug in the calling code.
				panic(fmt.Sprintf("validateRSPAndLVGs: LVG %q not found in lvgByName (invariant violation)", rspLVG.Name))
			}

			// Check if ThinPool exists in LVG.
			thinPoolFound := false
			for _, tp := range lvg.Spec.ThinPools {
				if tp.Name == rspLVG.ThinPoolName {
					thinPoolFound = true
					break
				}
			}
			if !thinPoolFound {
				return fmt.Errorf("LVMVolumeGroup %q: thinPool %q not found in Spec.ThinPools", rspLVG.Name, rspLVG.ThinPoolName)
			}
		}
	}

	return nil
}

// --- Construction helpers ---

// buildLVGByNodeMap builds a map of node name to LVG entries for the RSP.
// Only LVGs that are referenced in rsp.Spec.LVMVolumeGroups are included.
// LVGs are sorted by name per node for deterministic output.
func buildLVGByNodeMap(
	lvgs []snc.LVMVolumeGroup,
	rsp *v1alpha1.ReplicatedStoragePool,
) map[string][]v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	// Build RSP LVG reference lookup: lvgName -> thinPoolName (for LVMThin).
	rspLVGRef := make(map[string]string, len(rsp.Spec.LVMVolumeGroups))
	for _, ref := range rsp.Spec.LVMVolumeGroups {
		rspLVGRef[ref.Name] = ref.ThinPoolName
	}

	result := make(map[string][]v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup)

	for i := range lvgs {
		lvg := &lvgs[i]

		// Check if this LVG is referenced by the RSP.
		thinPoolName, referenced := rspLVGRef[lvg.Name]
		if !referenced {
			continue
		}

		// Get node name from LVG spec.
		nodeName := lvg.Spec.Local.NodeName
		if nodeName == "" {
			continue
		}

		// Check if LVG is unschedulable.
		_, unschedulable := lvg.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]

		// Determine readiness of the LVG (and thin pool if applicable).
		ready := isLVGReady(lvg, thinPoolName)

		entry := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          lvg.Name,
			ThinPoolName:  thinPoolName,
			Unschedulable: unschedulable,
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

// =============================================================================
// Single-call I/O helpers
// =============================================================================

// --- RSP ---

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

// --- LVG ---

// getSortedLVGsByRSP fetches LVGs referenced by the given RSP, sorted by name.
// Returns:
//   - lvgs: successfully found LVGs, sorted by name
//   - lvgsNotFoundErr: merged error for any NotFound cases (nil if all found)
//   - err: non-NotFound error (if any occurred, lvgs will be nil)
func (r *Reconciler) getSortedLVGsByRSP(ctx context.Context, rsp *v1alpha1.ReplicatedStoragePool) (
	lvgs []snc.LVMVolumeGroup,
	lvgsNotFoundErr error,
	err error,
) {
	if rsp == nil || len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, nil, nil
	}

	lvgs = make([]snc.LVMVolumeGroup, 0, len(rsp.Spec.LVMVolumeGroups))
	var notFoundErrs []error

	for _, lvgRef := range rsp.Spec.LVMVolumeGroups {
		var lvg snc.LVMVolumeGroup
		if err := r.cl.Get(ctx, client.ObjectKey{Name: lvgRef.Name}, &lvg); err != nil {
			if apierrors.IsNotFound(err) {
				notFoundErrs = append(notFoundErrs, err)
				continue
			}
			// Non-NotFound error - fail immediately.
			return nil, nil, err
		}
		lvgs = append(lvgs, lvg)
	}

	// Sort by name for deterministic output.
	sort.Slice(lvgs, func(i, j int) bool {
		return lvgs[i].Name < lvgs[j].Name
	})

	return lvgs, errors.Join(notFoundErrs...), nil
}

// --- Node ---

// getSortedNodes fetches nodes matching the given selector, sorted by name.
// The selector should include NodeLabelSelector and Zones requirements from RSP.
func (r *Reconciler) getSortedNodes(ctx context.Context, selector labels.Selector) ([]corev1.Node, error) {
	var list corev1.NodeList
	if err := r.cl.List(ctx, &list, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}

// --- Pod ---

// getAgentPods fetches all agent pods in the controller namespace.
func (r *Reconciler) getAgentPods(ctx context.Context) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := r.cl.List(ctx, &list,
		client.InNamespace(r.agentPodNamespace),
		client.MatchingLabels{"app": "agent"},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}
