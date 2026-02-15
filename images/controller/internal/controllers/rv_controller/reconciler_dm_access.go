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
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAccessReplicas
//

// ensureDatameshAccessReplicas coordinates datamesh Access replica membership:
// completes finished transitions, processes join requests, and processes leave requests.
func ensureDatameshAccessReplicas(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "datamesh-access-replicas")
	defer ef.OnEnd(&outcome)

	changed := false

	// Diskful member IDs — stable (only Access members are added/removed, never Diskful).
	diskfulMembers := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeDiskful
	})

	// Index Access-related pendings by ID for O(1) lookup.
	// Includes: join with type=Access, and leave for Access members.
	accessMembers := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeAccess
	})
	var pendingReplicaTransitions [32]*v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition
	for i := range rv.Status.DatameshPendingReplicaTransitions {
		prt := &rv.Status.DatameshPendingReplicaTransitions[i]

		// Skip non join/leave requests.
		if prt.Transition.Member == nil {
			continue
		}

		// Skip join requests with type != Access.
		if *prt.Transition.Member && prt.Transition.Type != v1alpha1.ReplicaTypeAccess {
			continue
		}

		// Skip leave requests for non-Access members.
		if !*prt.Transition.Member && !accessMembers.Contains(prt.ID()) {
			continue
		}

		pendingReplicaTransitions[prt.ID()] = prt
	}

	// Loop 1: existing transitions — complete or update progress.
	for i := len(rv.Status.DatameshTransitions) - 1; i >= 0; i-- {
		t := &rv.Status.DatameshTransitions[i]

		if t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica &&
			t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica {
			continue
		}

		replicaID := t.ReplicaID()

		completed, c := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, pendingReplicaTransitions[replicaID], diskfulMembers)
		changed = c || changed
		if completed {
			rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
			changed = true
		}

		// Mark as processed — loop 2 will not see this pending.
		pendingReplicaTransitions[replicaID] = nil
	}

	// Loop 2: remaining pendings — no active transition yet.
	for _, prt := range pendingReplicaTransitions {
		if prt == nil {
			continue
		}
		if *prt.Transition.Member {
			changed = ensureDatameshAddAccessReplica(rv, rvrs, prt, diskfulMembers, rsp) || changed
		} else {
			changed = ensureDatameshRemoveAccessReplica(rv, rvrs, prt, diskfulMembers) || changed
		}
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAccessReplicaTransitionProgress
//

// ensureDatameshAccessReplicaTransitionProgress checks confirmation progress for a single
// AddAccessReplica or RemoveAccessReplica transition and updates messages.
// Returns (completed, changed). Does NOT delete the transition — the caller handles that.
func ensureDatameshAccessReplicaTransitionProgress(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
	prt *v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition,
	diskfulMembers idset.IDSet,
) (completed, changed bool) {
	replicaID := t.ReplicaID()
	mustConfirm := diskfulMembers.Union(idset.Of(replicaID))

	// Replicas that have confirmed the transition.
	confirmed := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Status.DatameshRevision >= t.DatameshRevision
	}).Intersect(mustConfirm)

	// For RemoveAccessReplica: the leaving replica confirms by resetting revision to 0
	// (it left the datamesh and no longer tracks the revision).
	if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica {
		if rvr := findRVRByID(rvrs, replicaID); rvr != nil && rvr.Status.DatameshRevision == 0 {
			confirmed.Add(replicaID)
		}
	}

	if confirmed == mustConfirm {
		// Transition complete — set completion message.
		switch t.Type {
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica:
			changed = applyPendingReplicaTransitionMessage(prt, "Joined datamesh successfully")
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica:
			changed = applyPendingReplicaTransitionMessage(prt, "Left datamesh successfully")
		}
		return true, changed
	}

	// For AddAccessReplica: the subject replica is expected to have DRBDConfigured=False
	// with reason PendingDatameshJoin (it has not joined the datamesh yet) — not an error.
	var skipError func(uint8, *metav1.Condition) bool
	if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica {
		skipError = func(id uint8, cond *metav1.Condition) bool {
			return id == replicaID && cond.Reason == v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin
		}
	}

	// Transition in progress — update messages.
	changed = applyTransitionMessage(t,
		computeDatameshTransitionProgressMessage(rvrs, t.DatameshRevision, mustConfirm, confirmed, skipError,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
	)

	progress := fmt.Sprintf("%d/%d replicas confirmed revision %d",
		confirmed.Len(), mustConfirm.Len(), t.DatameshRevision)
	switch t.Type {
	case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica:
		changed = applyPendingReplicaTransitionMessage(prt, "Joining datamesh, "+progress) || changed
	case v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica:
		changed = applyPendingReplicaTransitionMessage(prt, "Leaving datamesh, "+progress) || changed
	}

	return false, changed
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAddAccessReplica
//

// ensureDatameshAddAccessReplica checks guards and creates an AddAccessReplica transition
// for a single pending join request. Returns true if rv was changed.
func ensureDatameshAddAccessReplica(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	prt *v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition,
	diskfulMembers idset.IDSet,
	rsp *rspView,
) bool {
	// Guard: Already a datamesh member — skip (transient state).
	if rv.Status.Datamesh.FindMemberByName(prt.Name) != nil {
		return false
	}

	// Guard: RV deleting — will not join (detach-only mode for join).
	if rv.DeletionTimestamp != nil {
		return applyPendingReplicaTransitionMessage(prt,
			"Will not join datamesh: volume is being deleted")
	}

	// Guard: VolumeAccess=Local — Access replicas are not allowed.
	if rv.Status.Configuration.VolumeAccess == v1alpha1.VolumeAccessLocal {
		return applyPendingReplicaTransitionMessage(prt,
			"Will not join datamesh: volumeAccess is Local")
	}

	// Guard: RVR must exist (ensureDatameshPendingReplicaTransitions guarantees this).
	rvr := findRVRByID(rvrs, prt.ID())
	if rvr == nil {
		return false
	}

	// Guard: Addresses must be populated (scheduling must be complete).
	if len(rvr.Status.Addresses) == 0 {
		return applyPendingReplicaTransitionMessage(prt,
			"Waiting for replica addresses to be populated")
	}

	// Guard: Any member already on this node — Access replica is redundant.
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.NodeName == rvr.Spec.NodeName {
			return applyPendingReplicaTransitionMessage(prt,
				fmt.Sprintf("Will not join datamesh: %s member %s already present on node %s",
					m.Type, m.Name, m.NodeName))
		}
	}

	// Guard: RSP must be available.
	if rsp == nil {
		return applyPendingReplicaTransitionMessage(prt,
			"Waiting for ReplicatedStoragePool to be available")
	}

	// Guard: Node must be in RSP eligible nodes.
	eligibleNode := rsp.FindEligibleNode(rvr.Spec.NodeName)
	if eligibleNode == nil {
		return applyPendingReplicaTransitionMessage(prt,
			fmt.Sprintf("Will not join datamesh: node %s is not in eligible nodes", rvr.Spec.NodeName))
	}
	zone := eligibleNode.ZoneName

	// All guards passed — add member and create AddAccessReplica transition.
	applyDatameshMember(rv, v1alpha1.ReplicatedVolumeDatameshMember{
		Name:      rvr.Name,
		Type:      v1alpha1.ReplicaTypeAccess,
		NodeName:  rvr.Spec.NodeName,
		Zone:      zone,
		Addresses: slices.Clone(rvr.Status.Addresses),
		Attached:  false,
	})

	// Create AddAccessReplica transition. Message is set below by the progress function.
	rv.Status.DatameshRevision++
	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
		v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
			DatameshRevision: rv.Status.DatameshRevision,
			ReplicaName:      rvr.Name,
			StartedAt:        metav1.Now(),
		},
	)

	// Set initial messages via the same function used for progress updates.
	t := &rv.Status.DatameshTransitions[len(rv.Status.DatameshTransitions)-1]
	ensureDatameshAccessReplicaTransitionProgress(rvrs, t, prt, diskfulMembers)

	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshRemoveAccessReplica
//

// ensureDatameshRemoveAccessReplica checks guards and creates a RemoveAccessReplica transition
// for a single pending leave request. Returns true if rv was changed.
func ensureDatameshRemoveAccessReplica(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	prt *v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition,
	diskfulMembers idset.IDSet,
) bool {
	// Guard: Not a datamesh member — skip (transient state).
	member := rv.Status.Datamesh.FindMemberByName(prt.Name)
	if member == nil {
		return false
	}

	// Guard: Member type must be Access (defensive — parent filters non-Access, but verify).
	if member.Type != v1alpha1.ReplicaTypeAccess {
		return false
	}

	// Guard: Member is attached — hard invariant, cannot remove.
	if member.Attached {
		return applyPendingReplicaTransitionMessage(prt,
			"Cannot leave datamesh: replica is attached, detach required first")
	}

	// All guards passed — remove the member from datamesh.
	removeDatameshMembers(rv, idset.Of(prt.ID()))

	// Create RemoveAccessReplica transition. Message is set below by the progress function.
	rv.Status.DatameshRevision++
	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
		v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
			DatameshRevision: rv.Status.DatameshRevision,
			ReplicaName:      prt.Name,
			StartedAt:        metav1.Now(),
		},
	)

	// Set initial messages via the same function used for progress updates.
	t := &rv.Status.DatameshTransitions[len(rv.Status.DatameshTransitions)-1]
	ensureDatameshAccessReplicaTransitionProgress(rvrs, t, prt, diskfulMembers)

	return true
}
