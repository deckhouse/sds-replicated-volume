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
	"slices"
	"strings"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// rvPredicates returns predicates for ReplicatedVolume events.
// Reacts to:
// - Generation changes (spec updates)
// - DeletionTimestamp changes (start of deletion)
// - ReplicatedStorageClassLabelKey label changes
// - Finalizers changes
func rvPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				if e.ObjectNew == nil || e.ObjectOld == nil {
					return true
				}

				// React to Generation change (Spec changes).
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}

				// React to DeletionTimestamp change (start of deletion).
				oldDT := e.ObjectOld.GetDeletionTimestamp()
				newDT := e.ObjectNew.GetDeletionTimestamp()
				if (oldDT == nil) != (newDT == nil) {
					return true
				}

				// React to ReplicatedStorageClassLabelKey label change.
				oldLabels := e.ObjectOld.GetLabels()
				newLabels := e.ObjectNew.GetLabels()
				oldV, oldOK := oldLabels[v1alpha1.ReplicatedStorageClassLabelKey]
				newV, newOK := newLabels[v1alpha1.ReplicatedStorageClassLabelKey]
				if oldOK != newOK || oldV != newV {
					return true
				}

				// React to Finalizers change.
				if !slices.Equal(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
					return true
				}

				// Ignore pure status updates to avoid reconcile loops.
				return false
			},
		},
	}
}

// rscPredicates returns predicates for ReplicatedStorageClass events.
// Reacts to:
// - ConfigurationGeneration changes (triggers RV configuration update)
func rscPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRSC, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStorageClass)
				newRSC, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStorageClass)
				if !okOld || !okNew || oldRSC == nil || newRSC == nil {
					return true
				}

				// React to ConfigurationGeneration change (configuration update).
				if oldRSC.Status.ConfigurationGeneration != newRSC.Status.ConfigurationGeneration {
					return true
				}

				return false
			},
		},
	}
}

// rvaPredicates returns predicates for ReplicatedVolumeAttachment events.
// Reacts to:
// - DeletionTimestamp changes (start of deletion — triggers detach/finalizer-removal flow)
// - Finalizers changes (finalizer management)
// - Attached condition status changes (affects rvShouldNotExist check)
func rvaPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				if e.ObjectOld == nil || e.ObjectNew == nil {
					return true
				}

				// React to DeletionTimestamp change.
				oldDT := e.ObjectOld.GetDeletionTimestamp()
				newDT := e.ObjectNew.GetDeletionTimestamp()
				if (oldDT == nil) != (newDT == nil) {
					return true
				}

				// React to Finalizers change.
				if !slices.Equal(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
					return true
				}

				// React to Attached condition status change.
				oldRVA, okOld := e.ObjectOld.(obju.StatusConditionObject)
				newRVA, okNew := e.ObjectNew.(obju.StatusConditionObject)
				if !okOld || !okNew || oldRVA == nil || newRVA == nil {
					return true
				}
				if !obju.AreConditionsEqualByStatus(oldRVA, newRVA, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType) {
					return true
				}

				return false
			},
		},
	}
}

// drbdrOpPredicates returns predicates for DRBDResourceOperation events.
// Only passes events for operations whose name ends with "-formation".
// Reacts to:
// - Create/Delete events (formation operation appeared or was removed)
// - Status.Phase changes (operation progress: Pending -> Running -> Succeeded/Failed)
// - Generation changes (spec was modified, need to re-validate parameters)
func drbdrOpPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
				return strings.HasSuffix(e.Object.GetName(), "-formation")
			},
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				if !strings.HasSuffix(e.ObjectNew.GetName(), "-formation") {
					return false
				}

				// React to Generation change (spec was modified, need to re-validate parameters).
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}

				// React to Status.Phase change (operation progress).
				oldDROp, okOld := e.ObjectOld.(*v1alpha1.DRBDResourceOperation)
				newDROp, okNew := e.ObjectNew.(*v1alpha1.DRBDResourceOperation)
				if !okOld || !okNew || oldDROp == nil || newDROp == nil {
					return true
				}

				return oldDROp.Status.Phase != newDROp.Status.Phase
			},
			DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
				return strings.HasSuffix(e.Object.GetName(), "-formation")
			},
		},
	}
}

// rvrPredicates returns predicates for ReplicatedVolumeReplica events.
// Reacts to:
// - Condition changes: Scheduled, DRBDConfigured, SatisfyEligibleNodes (formation progress, misplaced detection)
// - DatameshRequest changes (preconfiguration readiness)
// - DatameshRevision changes (rollout progress)
// - Addresses changes (network readiness during formation)
// - BackingVolume changes (backing volume state during formation and data bootstrap)
// - Peers changes (connectivity and replication state during formation)
// - DeletionTimestamp changes (deletion started)
// - Finalizers changes (cleanup)
func rvrPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRVR, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
				newRVR, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
				if !okOld || !okNew || oldRVR == nil || newRVR == nil {
					return true
				}

				// React to condition changes used during formation and normal operation:
				// - Scheduled: scheduling progress (preconfigure phase)
				// - DRBDConfigured: DRBD configuration progress (preconfigure + establish-connectivity phases)
				// - SatisfyEligibleNodes: misplaced replica detection (preconfigure phase)
				if !obju.AreConditionsSemanticallyEqual(
					oldRVR, newRVR,
					v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
					v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
				) {
					return true
				}

				// React to DatameshRequest change (preconfiguration readiness).
				if !oldRVR.Status.DatameshRequest.Equals(newRVR.Status.DatameshRequest) {
					return true
				}

				// React to DatameshRevision change (rollout progress).
				if oldRVR.Status.DatameshRevision != newRVR.Status.DatameshRevision {
					return true
				}

				// React to Addresses change (network readiness for formation).
				if !slices.EqualFunc(oldRVR.Status.Addresses, newRVR.Status.Addresses,
					func(a, b v1alpha1.DRBDResourceAddressStatus) bool {
						return a.SystemNetworkName == b.SystemNetworkName && a.Address == b.Address
					}) {
					return true
				}

				// React to BackingVolume changes (state and size during formation).
				oldBV, newBV := oldRVR.Status.BackingVolume, newRVR.Status.BackingVolume
				if (oldBV == nil) != (newBV == nil) ||
					(oldBV != nil && newBV != nil && (oldBV.State != newBV.State || !ptr.Equal(oldBV.Size, newBV.Size))) {
					return true
				}

				// React to Peers changes (connectivity and replication during formation).
				if !slices.EqualFunc(oldRVR.Status.Peers, newRVR.Status.Peers,
					func(a, b v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
						return a.Name == b.Name && a.Type == b.Type &&
							a.ConnectionState == b.ConnectionState && a.ReplicationState == b.ReplicationState
					}) {
					return true
				}

				// React to DeletionTimestamp change.
				oldDT := e.ObjectOld.GetDeletionTimestamp()
				newDT := e.ObjectNew.GetDeletionTimestamp()
				if (oldDT == nil) != (newDT == nil) {
					return true
				}

				// React to Finalizers change.
				if !slices.Equal(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
					return true
				}

				return false
			},
		},
	}
}
