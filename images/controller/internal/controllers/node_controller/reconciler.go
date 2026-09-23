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

package nodecontroller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl client.Client
	// dataNodeSelector is the module-wide data-node selector (dataNodes.nodeSelector).
	// Nodes matching it get the agent label unconditionally, which bootstraps the agent
	// (and the sds-node-configurator agent) on a clean cluster before any RSP exists.
	dataNodeSelector labels.Selector
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// Pre-computed raw merge patch payloads for setting/removing the agent node label.
// Using raw patches avoids fetching the full Node, DeepCopy, and expensive JSON merge patch
// diff computation. Each payload is a trivially small, deterministic JSON document.
var (
	rawPatchSetAgentLabel    = []byte(`{"metadata":{"labels":{"` + v1alpha1.AgentNodeLabelKey + `":""}}}`)
	rawPatchRemoveAgentLabel = []byte(`{"metadata":{"labels":{"` + v1alpha1.AgentNodeLabelKey + `":null}}}`)
)

// NewReconciler creates a Reconciler.
// dataNodeSelector is a validated label set; a nil/empty set matches all nodes.
func NewReconciler(cl client.Client, dataNodeSelector labels.Set) *Reconciler {
	return &Reconciler{
		cl:               cl,
		dataNodeSelector: labels.SelectorFromValidatedSet(dataNodeSelector),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Conditional target evaluation
//
// Reconciles a single Node by checking if it should have the AgentNodeLabelKey.
// A node should have the label if:
//   - it matches the module-wide dataNodes.nodeSelector, OR
//   - it is in at least one RSP's eligibleNodes, OR
//   - it has at least one DRBDResource (to prevent orphaning DRBD resources)
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	nodeName := req.Name

	// Check current label state (cheap, uses UnsafeDisableDeepCopy).
	presence, err := r.getNodeAgentLabelPresence(rf.Ctx(), nodeName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if !presence.exists {
		// Node was deleted, nothing to do.
		return rf.Done().ToCtrl()
	}

	// A node selected by dataNodes.nodeSelector needs the label regardless of RSP/DRBD state,
	// so the index lookups are skipped for it.
	shouldHaveLabel := presence.matchesDataNodeSelector
	if !shouldHaveLabel {
		var drbdCount, rspCount int

		// Check if node has any DRBDResources.
		drbdCount, err = r.getNumberOfDRBDResourcesByNode(rf.Ctx(), nodeName)
		if err != nil {
			return rf.Fail(err).ToCtrl()
		}

		// Check if node is in any RSP's eligibleNodes.
		// RSP eligibility is a subset of dataNodeSelector, so this only matters while RSP status
		// still lists a node that has just left the selector (selector narrowed or node relabeled):
		// the label is kept until rsp_controller drops the node from eligibleNodes.
		rspCount, err = r.getNumberOfRSPByEligibleNode(rf.Ctx(), nodeName)
		if err != nil {
			return rf.Fail(err).ToCtrl()
		}

		// Node should have label if it has any DRBDResource OR is in any RSP's eligibleNodes.
		shouldHaveLabel = drbdCount > 0 || rspCount > 0
	}

	// Check if node is already in sync:
	// - should have label AND label present with correct (empty) value, OR
	// - should not have label AND label absent.
	isInSync := (shouldHaveLabel && presence.labelValueCorrect) || (!shouldHaveLabel && !presence.hasLabel)
	if isInSync {
		return rf.Done().ToCtrl()
	}

	// Patch node label using a pre-computed raw merge patch.
	// No full node GET or DeepCopy needed: the patch bytes are trivially small and deterministic.
	// Without optimistic lock: we only touch a single label map key,
	// and Node objects change frequently from external sources like kubelet heartbeats,
	// so optimistic lock would cause constant 409 Conflict errors.
	var node corev1.Node
	node.Name = nodeName
	patch := rawPatchRemoveAgentLabel
	if shouldHaveLabel {
		patch = rawPatchSetAgentLabel
	}
	if err := r.cl.Patch(rf.Ctx(), &node, client.RawPatch(types.MergePatchType, patch)); err != nil {
		if apierrors.IsNotFound(err) {
			// Node was deleted between checks, nothing to do.
			return rf.Done().ToCtrl()
		}
		return rf.Fail(err).ToCtrl()
	}

	return rf.Done().ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// nodeAgentLabelPresence is the part of a Node observed by getNodeAgentLabelPresence.
type nodeAgentLabelPresence struct {
	exists                  bool
	hasLabel                bool
	labelValueCorrect       bool
	matchesDataNodeSelector bool
}

// getNodeAgentLabelPresence checks if a node exists, whether it has the AgentNodeLabelKey,
// whether the label value is the canonical empty string, and whether the node matches
// the data-node selector.
// Uses UnsafeDisableDeepCopy for performance: node labels are evaluated here and never
// leave the helper, so the shared cache object is not exposed to callers.
// A missing node is not an error: it yields a zero value with exists == false.
func (r *Reconciler) getNodeAgentLabelPresence(ctx context.Context, name string) (nodeAgentLabelPresence, error) {
	var unsafeNode corev1.Node
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &unsafeNode, client.UnsafeDisableDeepCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nodeAgentLabelPresence{}, nil
		}
		return nodeAgentLabelPresence{}, err
	}
	hasLabel := obju.HasLabel(&unsafeNode, v1alpha1.AgentNodeLabelKey)
	return nodeAgentLabelPresence{
		exists:                  true,
		hasLabel:                hasLabel,
		labelValueCorrect:       hasLabel && unsafeNode.Labels[v1alpha1.AgentNodeLabelKey] == "",
		matchesDataNodeSelector: r.dataNodeSelector.Matches(labels.Set(unsafeNode.Labels)),
	}, nil
}

// getNumberOfDRBDResourcesByNode returns the count of DRBDResource objects on the specified node.
// Uses index for efficient lookup and UnsafeDisableDeepCopy for performance.
func (r *Reconciler) getNumberOfDRBDResourcesByNode(ctx context.Context, nodeName string) (int, error) {
	var list v1alpha1.DRBDResourceList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldDRBDResourceByNodeName: nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// getNumberOfRSPByEligibleNode returns the count of RSP objects that have the specified node
// in their eligibleNodes list.
// Uses index for efficient lookup and UnsafeDisableDeepCopy for performance.
func (r *Reconciler) getNumberOfRSPByEligibleNode(ctx context.Context, nodeName string) (int, error) {
	var list v1alpha1.ReplicatedStoragePoolList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRSPByEligibleNodeName: nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}
