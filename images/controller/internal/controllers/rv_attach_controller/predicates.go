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

package rvattachcontroller

import (
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// replicatedVolumePredicate filters RV events so rv_attach_controller does not reconcile on its own status-only updates
// (desiredAttachTo/actuallyAttachedTo/allowTwoPrimaries), but still reacts to inputs that affect attach logic.
func replicatedVolumePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool {
			// Be conservative: don't reconcile on generic events (rare), rely on real create/update/delete.
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRV := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
			newRV := e.ObjectNew.(*v1alpha1.ReplicatedVolume)

			// Spec change (generation bump) can affect which storage class we load.
			if oldRV.Generation != newRV.Generation {
				return true
			}

			// Start of deletion must be observed (detach-only mode).
			if oldRV.DeletionTimestamp.IsZero() != newRV.DeletionTimestamp.IsZero() {
				return true
			}

			// Controller finalizer gate affects whether attachments are allowed.
			if obju.HasFinalizer(oldRV, v1alpha1.ControllerFinalizer) != obju.HasFinalizer(newRV, v1alpha1.ControllerFinalizer) {
				return true
			}

			// IOReady condition gates attachments; it is status-managed by another controller.
			oldIOReady := meta.IsStatusConditionTrue(oldRV.Status.Conditions, v1alpha1.ReplicatedVolumeCondIOReadyType)
			newIOReady := meta.IsStatusConditionTrue(newRV.Status.Conditions, v1alpha1.ReplicatedVolumeCondIOReadyType)
			return oldIOReady != newIOReady
		},
	}
}

// replicatedVolumeReplicaPredicate filters RVR events to only those that can affect RV attach/detach logic.
func replicatedVolumeReplicaPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRVR := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
			newRVR := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)

			// If controller owner reference is set later, allow this update so EnqueueRequestForOwner can start working.
			if metav1.GetControllerOf(oldRVR) == nil && metav1.GetControllerOf(newRVR) != nil {
				return true
			}

			// Deletion start affects eligibility of a node for new attachments.
			if oldRVR.DeletionTimestamp.IsZero() != newRVR.DeletionTimestamp.IsZero() {
				return true
			}

			// Node/type changes affect locality checks and promotion flow.
			if oldRVR.Spec.NodeName != newRVR.Spec.NodeName {
				return true
			}
			if oldRVR.Spec.Type != newRVR.Spec.Type {
				return true
			}

			// Local volume access requires Diskful actualType on requested node.
			oldActualType := oldRVR.Status.ActualType
			newActualType := newRVR.Status.ActualType
			if oldActualType != newActualType {
				return true
			}

			// actuallyAttachedTo is derived from DRBD role == Primary.
			oldRole := rvrDRBDRole(oldRVR)
			newRole := rvrDRBDRole(newRVR)
			if oldRole != newRole {
				return true
			}

			// allowTwoPrimaries readiness gate is derived from DRBD Actual.AllowTwoPrimaries.
			if rvrAllowTwoPrimariesActual(oldRVR) != rvrAllowTwoPrimariesActual(newRVR) {
				return true
			}

			// RVA ReplicaReady mirrors replica condition Ready, so changes must trigger reconcile.
			// Compare (status, reason, message) to keep mirroring accurate even when status doesn't change.
			oldCond := meta.FindStatusCondition(oldRVR.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
			newCond := meta.FindStatusCondition(newRVR.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
			return !obju.ConditionSemanticallyEqual(oldCond, newCond)
		},
	}
}

// replicatedVolumeAttachmentPredicate filters RVA events so we don't reconcile on our own status-only updates.
// It still reacts to create/delete, start of deletion and finalizer changes.
func replicatedVolumeAttachmentPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRVA := e.ObjectOld.(*v1alpha1.ReplicatedVolumeAttachment)
			newRVA := e.ObjectNew.(*v1alpha1.ReplicatedVolumeAttachment)

			// Start of deletion affects desiredAttachTo and finalizer reconciliation.
			if oldRVA.DeletionTimestamp.IsZero() != newRVA.DeletionTimestamp.IsZero() {
				return true
			}

			// Even though spec fields are immutable, generation bump is a safe signal for any spec-level changes.
			if oldRVA.Generation != newRVA.Generation {
				return true
			}

			// Finalizers are important for safe detach/cleanup.
			if !slices.Equal(oldRVA.Finalizers, newRVA.Finalizers) {
				return true
			}

			return false
		},
	}
}

func rvrDRBDRole(rvr *v1alpha1.ReplicatedVolumeReplica) string {
	if rvr == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
		return ""
	}
	return rvr.Status.DRBD.Status.Role
}

func rvrAllowTwoPrimariesActual(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Actual == nil {
		return false
	}
	return rvr.Status.DRBD.Actual.AllowTwoPrimaries
}

// Note: condition equality is delegated to v1alpha1.ConditionSpecAgnosticEqual.
