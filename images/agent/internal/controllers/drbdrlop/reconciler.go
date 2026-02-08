/*
Copyright 2025 Flant JSC

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

package drbdrlop

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ListOperationReconciler reconciles DRBDResourceListOperation objects (multi-resource operations).
type ListOperationReconciler struct {
	cl       client.Client
	nodeName string
}

// NewListOperationReconciler creates a new ListOperationReconciler.
func NewListOperationReconciler(cl client.Client, nodeName string) *ListOperationReconciler {
	return &ListOperationReconciler{
		cl:       cl,
		nodeName: nodeName,
	}
}

// Reconcile reconciles a DRBDResourceListOperation.
// Reconcile pattern: Pure orchestration — dispatch by operation type to reconcile* helpers.
func (r *ListOperationReconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)
	rf.Log().V(1).Info("Reconciling DRBDResourceListOperation", "name", req.Name)

	op, err := r.getOperation(rf.Ctx(), req.NamespacedName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if op == nil {
		return rf.Done().ToCtrl()
	}

	// Find the local DRBDR for this node from the operation's resource names.
	// We need this early to check if our result is already terminal.
	localDRBDR, err := r.getLocalDRBDRForListOperation(rf.Ctx(), op)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if localDRBDR == nil {
		// No local DRBDR in this operation — nothing to do on this node
		return rf.Done().ToCtrl()
	}

	// Terminal check: our result (keyed by localDRBDR.Name) is Succeeded or Failed
	if isResultTerminal(op, localDRBDR.Name) {
		rf.Log().V(1).Info("Result for local DRBDR is terminal, skipping", "drbdResourceName", localDRBDR.Name)
		return rf.Done().ToCtrl()
	}

	switch op.Spec.Type {
	case v1alpha1.DRBDResourceListOperationConfigurePeers:
		return r.reconcileConfigurePeers(rf.Ctx(), op, localDRBDR).ToCtrl()
	default:
		return r.reconcileUnsupported(rf.Ctx(), op, localDRBDR, op.Spec.Type).ToCtrl()
	}
}

// reconcileConfigurePeers configures spec.peers for the local DRBDResource from
// other resources' status.addresses.
func (r *ListOperationReconciler) reconcileConfigurePeers(
	ctx context.Context,
	op *v1alpha1.DRBDResourceListOperation,
	localDRBDR *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "ConfigurePeers")
	defer rf.OnEnd(&outcome)

	allDRBDRs, err := r.buildAllDRBDRsForConfigurePeers(ctx, op, localDRBDR)
	if err != nil {
		if applyErr := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseFailed, err.Error()); applyErr != nil {
			return rf.Fail(applyErr)
		}
		return rf.Fail(err)
	}

	// Apply Running result before doing work
	if applyErr := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseRunning, ""); applyErr != nil {
		return rf.Fail(applyErr)
	}

	peers, err := computePeersForConfigurePeers(localDRBDR, allDRBDRs)
	if err != nil {
		if applyErr := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseFailed, err.Error()); applyErr != nil {
			return rf.Fail(applyErr)
		}
		return rf.Fail(err)
	}

	drbdrBase := localDRBDR.DeepCopy()
	applyPeersToDRBDR(localDRBDR, peers)
	if err := r.patchDRBDR(ctx, localDRBDR, drbdrBase, false); err != nil {
		if applyErr := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseFailed, err.Error()); applyErr != nil {
			return rf.Fail(applyErr)
		}
		return rf.Fail(err)
	}

	// Apply Succeeded result
	if err := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseSucceeded, ""); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// reconcileUnsupported marks the result for the local DRBDR as Failed.
func (r *ListOperationReconciler) reconcileUnsupported(ctx context.Context, op *v1alpha1.DRBDResourceListOperation, localDRBDR *v1alpha1.DRBDResource, opType v1alpha1.DRBDResourceListOperationType) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "Unsupported")
	defer rf.OnEnd(&outcome)

	if err := r.applyResultSSA(ctx, op, localDRBDR.Name, v1alpha1.DRBDOperationPhaseFailed, fmt.Sprintf("operation type %s not implemented", opType)); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// getOperation fetches the DRBDResourceListOperation by key. Returns (nil, nil) if not found.
func (r *ListOperationReconciler) getOperation(ctx context.Context, key client.ObjectKey) (*v1alpha1.DRBDResourceListOperation, error) {
	op := &v1alpha1.DRBDResourceListOperation{}
	if err := r.cl.Get(ctx, key, op); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return op, nil
}

// getDRBDR fetches a DRBDResource by name. Returns (nil, nil) if not found.
func (r *ListOperationReconciler) getDRBDR(ctx context.Context, name string) (*v1alpha1.DRBDResource, error) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, drbdr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return drbdr, nil
}

// getLocalDRBDRForListOperation lists DRBDResources on this node (index), intersects with operation's resource names.
// Returns (nil, nil) when intersection is 0 (no work on this node); (nil, err) when >1 (invalid); (local, nil) when exactly 1.
func (r *ListOperationReconciler) getLocalDRBDRForListOperation(ctx context.Context, op *v1alpha1.DRBDResourceListOperation) (*v1alpha1.DRBDResource, error) {
	namesSet := make(map[string]struct{}, len(op.Spec.DRBDResourceNames))
	for _, n := range op.Spec.DRBDResourceNames {
		namesSet[n] = struct{}{}
	}
	list := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldDRBDRByNodeName: r.nodeName,
	}); err != nil {
		return nil, err
	}
	var matches []*v1alpha1.DRBDResource
	for i := range list.Items {
		dr := &list.Items[i]
		if _, ok := namesSet[dr.Name]; ok {
			matches = append(matches, dr)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("multiple DRBDResources on this node in same list operation")
	}
}

// buildAllDRBDRsForConfigurePeers fetches all DRBDResources named in the operation (using local when name matches);
// each must have status.addresses.
func (r *ListOperationReconciler) buildAllDRBDRsForConfigurePeers(ctx context.Context, op *v1alpha1.DRBDResourceListOperation, localDRBDR *v1alpha1.DRBDResource) ([]*v1alpha1.DRBDResource, error) {
	names := op.Spec.DRBDResourceNames
	out := make([]*v1alpha1.DRBDResource, 0, len(names))
	for _, name := range names {
		var dr *v1alpha1.DRBDResource
		if name == localDRBDR.Name {
			dr = localDRBDR
		} else {
			got, err := r.getDRBDR(ctx, name)
			if err != nil {
				return nil, err
			}
			if got == nil {
				return nil, fmt.Errorf("DRBDResource %q not found", name)
			}
			dr = got
		}
		if len(dr.Status.Addresses) == 0 {
			return nil, fmt.Errorf("DRBDResource %q has no status.addresses", name)
		}
		out = append(out, dr)
	}
	return out, nil
}

// isResultTerminal returns true if the operation has a result for the given drbdResourceName in a terminal phase.
func isResultTerminal(op *v1alpha1.DRBDResourceListOperation, drbdResourceName string) bool {
	if op.Status == nil {
		return false
	}
	for i := range op.Status.Results {
		r := &op.Status.Results[i]
		if r.DRBDResourceName == drbdResourceName {
			switch r.Phase {
			case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
				return true
			}
			return false
		}
	}
	return false
}

// applyResultSSA applies a single result entry for drbdResourceName via Server-Side Apply.
// Each node owns its entry via FieldOwner(r.nodeName).
func (r *ListOperationReconciler) applyResultSSA(
	ctx context.Context,
	op *v1alpha1.DRBDResourceListOperation,
	drbdResourceName string,
	phase v1alpha1.DRBDOperationPhase,
	message string,
) error {
	now := metav1.NewTime(time.Now())

	result := v1alpha1.DRBDResourceListOperationResult{
		DRBDResourceName: drbdResourceName,
		Phase:            phase,
		Message:          message,
	}

	switch phase {
	case v1alpha1.DRBDOperationPhaseRunning:
		result.StartedAt = &now
	case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
		result.CompletedAt = &now
	}

	// Build a minimal apply-configuration object for SSA
	applyObj := &v1alpha1.DRBDResourceListOperation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "DRBDResourceListOperation",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: op.Name,
		},
		Status: &v1alpha1.DRBDResourceListOperationStatus{
			Results: []v1alpha1.DRBDResourceListOperationResult{result},
		},
	}

	return r.cl.Status().Patch(ctx, applyObj, client.Apply, client.FieldOwner(r.nodeName), client.ForceOwnership)
}

// patchDRBDR patches the main spec of a DRBDResource (used by ConfigurePeers to set spec.peers).
func (r *ListOperationReconciler) patchDRBDR(
	ctx context.Context,
	obj, base *v1alpha1.DRBDResource,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}
