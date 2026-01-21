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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

const (
	// RVControllerName is the controller name for rv_controller.
	RVControllerName = "rv_controller"
)

func BuildController(mgr manager.Manager, worldGate world.WorldGate, worldBus world.WorldBus) error {
	cl := mgr.GetClient()
	_ = worldBus // TODO: use when needed

	rec := NewReconciler(cl, worldGate)

	return builder.ControllerManagedBy(mgr).
		Named(RVControllerName).
		For(
			&v1alpha1.ReplicatedVolume{},
			builder.WithPredicates(
				predicate.Funcs{
					UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
						if e.ObjectNew == nil || e.ObjectOld == nil {
							return true
						}

						// If reconciliation uses status.conditions (or any generation-driven logic),
						// react to generation changes for spec-driven updates.
						if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
							return true
						}

						// If RV deletion started, reconcile to execute finalization paths (metadata-only updates don't bump generation).
						oldDT := e.ObjectOld.GetDeletionTimestamp()
						newDT := e.ObjectNew.GetDeletionTimestamp()
						if (oldDT == nil) != (newDT == nil) {
							return true
						}

						// The controller enforces this label to match spec.replicatedStorageClassName.
						// Metadata-only updates don't bump generation, so react to changes of this single label key.
						oldLabels := e.ObjectOld.GetLabels()
						newLabels := e.ObjectNew.GetLabels()
						oldV, oldOK := oldLabels[v1alpha1.ReplicatedStorageClassLabelKey]
						newV, newOK := newLabels[v1alpha1.ReplicatedStorageClassLabelKey]
						if oldOK != newOK || oldV != newV {
							return true
						}

						// Ignore pure status updates to avoid reconcile loops.
						return false
					},
				},
			),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}
