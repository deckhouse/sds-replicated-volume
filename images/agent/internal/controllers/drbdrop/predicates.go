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

package drbdrop

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// operationPredicates returns predicates for the DRBDResourceOperation controller.
// Only operations whose Spec.NodeName matches nodeName trigger reconciliation;
// the cache is also filtered by nodeName, this is defense-in-depth.
func operationPredicates(nodeName string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				op, ok := e.Object.(*v1alpha1.DRBDResourceOperation)
				if !ok {
					return false
				}
				return op.Spec.NodeName == nodeName && needsReconcile(op)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				op, ok := e.ObjectNew.(*v1alpha1.DRBDResourceOperation)
				if !ok {
					return false
				}
				return op.Spec.NodeName == nodeName && needsReconcile(op)
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				// No need to reconcile deleted operations
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				op, ok := e.Object.(*v1alpha1.DRBDResourceOperation)
				if !ok {
					return false
				}
				return op.Spec.NodeName == nodeName && needsReconcile(op)
			},
		},
	}
}

// needsReconcile returns true if the operation must reach the reconciler:
// either it is not yet in a terminal phase, or it carries the admin_lock
// finalizer that still needs to be cleaned up on deletion.
func needsReconcile(op *v1alpha1.DRBDResourceOperation) bool {
	if !isOperationTerminal(op) {
		return true
	}
	return op.DeletionTimestamp != nil && hasFinalizer(op, AdminLockFinalizer)
}

// isOperationTerminal returns true if the operation is in a terminal state.
func isOperationTerminal(op *v1alpha1.DRBDResourceOperation) bool {
	switch op.Status.Phase {
	case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
		return true
	}
	return false
}
