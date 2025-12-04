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

package rvstatusconfigsharedsecret

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(RVStatusConfigSharedSecretControllerName).WithName("Reconciler"),
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVStatusConfigSharedSecretControllerName).
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &v1alpha3.ReplicatedVolume{}, handler.OnlyControllerOwner()),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Only enqueue if RVR has UnsupportedAlgorithm error
					if e.ObjectNew == nil {
						return false
					}
					rvr, ok := e.ObjectNew.(*v1alpha3.ReplicatedVolumeReplica)
					if !ok {
						return false
					}
					return HasUnsupportedAlgorithmError(rvr)
				},
				CreateFunc: func(e event.CreateEvent) bool {
					// Only enqueue if RVR has UnsupportedAlgorithm error
					if e.Object == nil {
						return false
					}
					rvr, ok := e.Object.(*v1alpha3.ReplicatedVolumeReplica)
					if !ok {
						return false
					}
					return HasUnsupportedAlgorithmError(rvr)
				},
			}),
		).
		Complete(rec)
}

// HasUnsupportedAlgorithmError checks if RVR has SharedSecretAlgSelectionError in drbd.errors
func HasUnsupportedAlgorithmError(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Errors == nil {
		return false
	}
	return rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil
}
