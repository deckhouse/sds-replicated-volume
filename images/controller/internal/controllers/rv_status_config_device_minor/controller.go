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

package rvstatusconfigdeviceminor

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
	// RVStatusConfigDeviceMinorControllerName is the controller name for rv_status_config_device_minor controller.
	RVStatusConfigDeviceMinorControllerName = "rv_status_config_device_minor_controller"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()
	log := mgr.GetLogger().WithName(RVStatusConfigDeviceMinorControllerName)

	// Create cache initializer that will populate the cache after leader election.
	// This ensures the cache is populated with the latest state right before
	// the controller starts processing events, avoiding stale cache issues.
	cacheSource := NewCacheInitializer(mgr)

	if err := mgr.Add(cacheSource); err != nil {
		return fmt.Errorf("adding cache initializer runnable: %w", err)
	}

	rec := NewReconciler(
		cl,
		log.WithName("Reconciler"),
		cacheSource,
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVStatusConfigDeviceMinorControllerName).
		For(
			&v1alpha1.ReplicatedVolume{},
			builder.WithPredicates(
				predicate.Funcs{
					CreateFunc: func(_ event.TypedCreateEvent[client.Object]) bool {
						return true
					},
					UpdateFunc: func(_ event.TypedUpdateEvent[client.Object]) bool {
						// deviceMinor can only be changed once, by this controller
						return false
					},
					DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
						// Release device minor from cache if available.
						// If cache is not ready yet, that's fine - deletions during startup
						// will be handled correctly when the cache is initialized.
						if cache := cacheSource.DeviceMinorCacheOrNil(); cache != nil {
							cache.Release(e.Object.GetName())
						}
						return false
					},
					GenericFunc: func(event.TypedGenericEvent[client.Object]) bool {
						return false
					},
				},
			),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

func deviceMinor(rv *v1alpha1.ReplicatedVolume) (int, bool) {
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil && rv.Status.DRBD.Config.DeviceMinor != nil {
		return int(*rv.Status.DRBD.Config.DeviceMinor), true
	}
	return 0, false
}
