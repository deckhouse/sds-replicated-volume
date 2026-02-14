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

package rvcontroller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: create-access-replicas
//

// reconcileCreateAccessReplicas creates Access RVRs for Active RVAs on nodes
// that do not yet have any RVR (any type).
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileCreateAccessReplicas(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "create-access-replicas")
	defer rf.OnEnd(&outcome)

	// Guard: RV is deleting — do not create new replicas (detach-only mode).
	if rv.DeletionTimestamp != nil {
		return rf.Continue()
	}

	// Guard: VolumeAccess is Local — Access replicas are not allowed.
	if rv.Status.Configuration.VolumeAccess == v1alpha1.VolumeAccessLocal {
		return rf.Continue()
	}

	// Guard: RSP must be available.
	if rsp == nil {
		return rf.Continue()
	}

	// Build set of node names that already have an RVR (any type, including deleting).
	rvrNodes := make(map[string]struct{}, len(*rvrs))
	for _, rvr := range *rvrs {
		if rvr.Spec.NodeName != "" {
			rvrNodes[rvr.Spec.NodeName] = struct{}{}
		}
	}

	for _, rva := range rvas {
		// Only Active (non-deleting) RVAs.
		if rva.DeletionTimestamp != nil {
			continue
		}

		nodeName := rva.Spec.NodeName

		// Skip if any RVR already exists on this node (any type).
		if _, exists := rvrNodes[nodeName]; exists {
			continue
		}

		// Check eligible node: must be in RSP, nodeReady, agentReady.
		en := rsp.FindEligibleNode(nodeName)
		if en == nil || !en.NodeReady || !en.AgentReady {
			continue
		}

		// Create Access RVR.
		_, err := r.createAccessRVR(rf.Ctx(), rv, rvrs, nodeName)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				rf.Log().Info("Access RVR already exists, requeueing", "node", nodeName)
				return rf.DoneAndRequeue()
			}
			return rf.Failf(err, "creating Access RVR for node %s", nodeName)
		}

		// Mark node as covered so duplicate RVAs on the same node produce only one creation.
		rvrNodes[nodeName] = struct{}{}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: delete-access-replicas
//

// reconcileDeleteAccessReplicas deletes Access RVRs that are no longer needed:
// redundant (another datamesh member on the same node) or unused (no active RVA).
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileDeleteAccessReplicas(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "delete-access-replicas")
	defer rf.OnEnd(&outcome)

	// Build set of nodes with Active (non-deleting) RVAs.
	activeRVANodes := make(map[string]struct{}, len(rvas))
	for _, rva := range rvas {
		if rva.DeletionTimestamp == nil {
			activeRVANodes[rva.Spec.NodeName] = struct{}{}
		}
	}

	// Build idset of attached datamesh members.
	attached := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Attached
	})

	// Build idset of IDs with active Detach or AddAccessReplica transitions.
	// Detach: avoid deleting while demote is in progress (churn).
	// AddAccessReplica: avoid deleting while join is in progress (would waste a full Add+Remove cycle).
	transitions := idset.FromFunc(rv.Status.DatameshTransitions, func(t v1alpha1.ReplicatedVolumeDatameshTransition) (uint8, bool) {
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach ||
			t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica {
			return t.ReplicaID(), true
		}
		return 0, false
	})

	for _, rvr := range *rvrs {
		// Only Access replicas.
		if rvr.Spec.Type != v1alpha1.ReplicaTypeAccess {
			continue
		}

		// Skip already deleting.
		if rvr.DeletionTimestamp != nil {
			continue
		}

		// Hard invariant: do not delete attached replicas.
		if attached.Contains(rvr.ID()) {
			continue
		}

		// Do not delete while Detach or AddAccessReplica is in progress.
		if transitions.Contains(rvr.ID()) {
			continue
		}

		// Check redundancy: another datamesh member exists on the same node.
		redundant := false
		for i := range rv.Status.Datamesh.Members {
			m := &rv.Status.Datamesh.Members[i]
			if m.NodeName == rvr.Spec.NodeName && m.Name != rvr.Name {
				redundant = true
				break
			}
		}

		// Check unused: no Active RVA on this node.
		_, hasActiveRVA := activeRVANodes[rvr.Spec.NodeName]

		// Delete if redundant or unused.
		if !redundant && hasActiveRVA {
			continue
		}

		if err := r.deleteRVR(rf.Ctx(), rvr); err != nil {
			return rf.Failf(err, "deleting Access RVR %s", rvr.Name)
		}
	}

	return rf.Continue()
}
