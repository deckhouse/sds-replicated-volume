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

package rvscontroller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/controlleroptions"
)

const RVSControllerName = "rvs-controller"

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetScheme())

	return builder.ControllerManagedBy(mgr).
		Named(RVSControllerName).
		For(&v1alpha1.ReplicatedVolumeSnapshot{}, builder.WithPredicates(
			rvsPredicates()...,
		)).
		Watches(
			&v1alpha1.ReplicatedVolumeReplicaSnapshot{},
			handler.EnqueueRequestsFromMapFunc(mapRVRSToRVS),
			builder.WithPredicates(rvrsPredicates()...),
		).
		Owns(&v1alpha1.DRBDResource{}, builder.WithPredicates(syncDRBDRPredicates()...)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(rec)
}

func mapRVRSToRVS(_ context.Context, obj client.Object) []reconcile.Request {
	rvrs, ok := obj.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
	if !ok || rvrs == nil || rvrs.Spec.ReplicatedVolumeSnapshotName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: rvrs.Spec.ReplicatedVolumeSnapshotName}}}
}
