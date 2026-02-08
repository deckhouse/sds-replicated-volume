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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// listOperationPredicates returns predicates for the DRBDResourceListOperation controller.
// We filter out events where all results in the operation are terminal (all resources done).
// The per-node terminal check (this node's result) is done in Reconcile.
func listOperationPredicates(nodeName string) []predicate.Predicate {
	_ = nodeName // reserved for future per-node predicate filtering
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				op, ok := e.Object.(*v1alpha1.DRBDResourceListOperation)
				if !ok {
					return false
				}
				return !areAllResultsTerminal(op)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				op, ok := e.ObjectNew.(*v1alpha1.DRBDResourceListOperation)
				if !ok {
					return false
				}
				return !areAllResultsTerminal(op)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				op, ok := e.Object.(*v1alpha1.DRBDResourceListOperation)
				if !ok {
					return false
				}
				return !areAllResultsTerminal(op)
			},
		},
	}
}

// areAllResultsTerminal returns true if every resource in spec.drbdResourceNames has a
// result with a terminal phase (Succeeded or Failed). This is a coarse filter; the precise
// per-node check is done in Reconcile.
func areAllResultsTerminal(op *v1alpha1.DRBDResourceListOperation) bool {
	if op.Status == nil || len(op.Status.Results) == 0 {
		return false
	}
	if len(op.Status.Results) < len(op.Spec.DRBDResourceNames) {
		return false
	}

	terminalSet := make(map[string]struct{}, len(op.Status.Results))
	for i := range op.Status.Results {
		r := &op.Status.Results[i]
		switch r.Phase {
		case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
			terminalSet[r.DRBDResourceName] = struct{}{}
		}
	}

	for _, name := range op.Spec.DRBDResourceNames {
		if _, ok := terminalSet[name]; !ok {
			return false
		}
	}
	return true
}
