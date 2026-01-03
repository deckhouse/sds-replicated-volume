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

package rvcontroller

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// RVControllerName is the controller name for rv_controller.
	RVControllerName = "rv_controller"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	// Initialize deviceMinor idpool after leader election (used for deviceMinor assignment).
	poolSource := NewDeviceMinorPoolInitializer(mgr)
	if err := mgr.Add(poolSource); err != nil {
		return fmt.Errorf("adding cache initializer runnable: %w", err)
	}

	rec := NewReconciler(
		cl,
		poolSource,
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVControllerName).
		For(
			&v1alpha1.ReplicatedVolume{},
			builder.WithPredicates(
				predicate.Funcs{
					UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
						oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
						newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
						if !okOld || !okNew || oldRV == nil || newRV == nil {
							// Be conservative: if we can't type-assert, allow reconcile.
							return true
						}

						// Trigger reconcile if storage class label is not in sync.
						if !newRV.IsStorageClassLabelInSync() {
							return true
						}

						return false
					},
				},
			),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}
