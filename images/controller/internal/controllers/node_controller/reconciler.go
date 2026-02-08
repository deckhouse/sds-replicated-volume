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
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// Pre-computed raw merge patch payloads for setting/removing the agent node label.
// Using raw patches avoids fetching the full Node, DeepCopy, and expensive JSON merge patch
// diff computation. Each payload is a trivially small, deterministic JSON document.
var (
	rawPatchSetAgentLabel    = []byte(`{"metadata":{"labels":{"` + v1alpha1.AgentNodeLabelKey + `":""}}}`)
	rawPatchRemoveAgentLabel = []byte(`{"metadata":{"labels":{"` + v1alpha1.AgentNodeLabelKey + `":null}}}`)
)

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Conditional target evaluation
//
// Reconciles a single Node by checking if it should have the AgentNodeLabelKey.
// A node should have the label if:
//   - it is in at least one RSP's eligibleNodes, OR
//   - it has at least one DRBDResource (to prevent orphaning DRBD resources)
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	nodeName := req.Name

	// Check current label state (cheap, uses UnsafeDisableDeepCopy).
	nodeExists, hasLabel, labelValueCorrect, err := r.getNodeAgentLabelPresence(rf.Ctx(), nodeName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if !nodeExists {
		// Node was deleted, nothing to do.
		return rf.Done().ToCtrl()
	}

	// Check if node has any DRBDResources.
	drbdCount, err := r.getNumberOfDRBDResourcesByNode(rf.Ctx(), nodeName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Check if node is in any RSP's eligibleNodes.
	rspCount, err := r.getNumberOfRSPByEligibleNode(rf.Ctx(), nodeName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Node should have label if it has any DRBDResource OR is in any RSP's eligibleNodes.
	shouldHaveLabel := drbdCount > 0 || rspCount > 0

	// Check if node is already in sync:
	// - should have label AND label present with correct (empty) value, OR
	// - should not have label AND label absent.
	isInSync := (shouldHaveLabel && labelValueCorrect) || (!shouldHaveLabel && !hasLabel)
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

// getNodeAgentLabelPresence checks if a node exists, whether it has the AgentNodeLabelKey,
// and whether the label value is the canonical empty string.
// Uses UnsafeDisableDeepCopy for performance since we only need to read the label.
// Returns (exists, hasLabel, labelValueCorrect, err).
func (r *Reconciler) getNodeAgentLabelPresence(ctx context.Context, name string) (bool, bool, bool, error) {
	var unsafeNode corev1.Node
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &unsafeNode, client.UnsafeDisableDeepCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return false, false, false, nil
		}
		return false, false, false, err
	}
	hasLabel := obju.HasLabel(&unsafeNode, v1alpha1.AgentNodeLabelKey)
	labelValueCorrect := hasLabel && unsafeNode.Labels[v1alpha1.AgentNodeLabelKey] == ""
	return true, hasLabel, labelValueCorrect, nil
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
