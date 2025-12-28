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

package rvattachcontroller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"k8s.io/apimachinery/pkg/api/meta"

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
			oldRV, ok := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
			newRV, ok2 := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
			if !ok || !ok2 {
				// If types are unexpected, do not accidentally drop the event.
				return true
			}

			// Spec change (generation bump) can affect which storage class we load.
			if oldRV.Generation != newRV.Generation {
				return true
			}

			// Start of deletion must be observed (detach-only mode).
			if oldRV.DeletionTimestamp.IsZero() != newRV.DeletionTimestamp.IsZero() {
				return true
			}

			// Controller finalizer gate affects whether attachments are allowed.
			if v1alpha1.HasControllerFinalizer(oldRV) != v1alpha1.HasControllerFinalizer(newRV) {
				return true
			}

			// IOReady condition gates attachments; it is status-managed by another controller.
			oldIOReady := oldRV.Status != nil && meta.IsStatusConditionTrue(oldRV.Status.Conditions, v1alpha1.ConditionTypeRVIOReady)
			newIOReady := newRV.Status != nil && meta.IsStatusConditionTrue(newRV.Status.Conditions, v1alpha1.ConditionTypeRVIOReady)
			if oldIOReady != newIOReady {
				return true
			}

			return false
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
			oldRVR, ok := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
			newRVR, ok2 := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok || !ok2 {
				return true
			}

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
			oldActualType := v1alpha1.ReplicaType("")
			if oldRVR.Status != nil {
				oldActualType = oldRVR.Status.ActualType
			}
			newActualType := v1alpha1.ReplicaType("")
			if newRVR.Status != nil {
				newActualType = newRVR.Status.ActualType
			}
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

			return false
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
			oldRVA, ok := e.ObjectOld.(*v1alpha1.ReplicatedVolumeAttachment)
			newRVA, ok2 := e.ObjectNew.(*v1alpha1.ReplicatedVolumeAttachment)
			if !ok || !ok2 {
				return true
			}

			// Start of deletion affects desiredAttachTo and finalizer reconciliation.
			if oldRVA.DeletionTimestamp.IsZero() != newRVA.DeletionTimestamp.IsZero() {
				return true
			}

			// Even though spec fields are immutable, generation bump is a safe signal for any spec-level changes.
			if oldRVA.Generation != newRVA.Generation {
				return true
			}

			// Finalizers are important for safe detach/cleanup.
			if !sliceEqual(oldRVA.Finalizers, newRVA.Finalizers) {
				return true
			}

			return false
		},
	}
}

func rvrDRBDRole(rvr *v1alpha1.ReplicatedVolumeReplica) string {
	if rvr == nil || rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
		return ""
	}
	return rvr.Status.DRBD.Status.Role
}

func rvrAllowTwoPrimariesActual(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr == nil || rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Actual == nil {
		return false
	}
	return rvr.Status.DRBD.Actual.AllowTwoPrimaries
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
