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

package rvrscontroller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/controlleroptions"
)

const RVRSControllerName = "rvrs-controller"

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetScheme())

	return builder.ControllerManagedBy(mgr).
		Named(RVRSControllerName).
		For(&v1alpha1.ReplicatedVolumeReplicaSnapshot{}, builder.WithPredicates(
			rvrsPredicates()...,
		)).
		Watches(
			&snc.LVMLogicalVolumeSnapshot{},
			handler.EnqueueRequestsFromMapFunc(mapLLVSToRVRS(cl)),
			builder.WithPredicates(llvsPredicates()...),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(rec)
}

func mapLLVSToRVRS(_ client.Client) handler.MapFunc {
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		llvs, ok := obj.(*snc.LVMLogicalVolumeSnapshot)
		if !ok || llvs == nil {
			return nil
		}
		for _, ref := range llvs.GetOwnerReferences() {
			if ref.Kind == "ReplicatedVolumeReplicaSnapshot" {
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: ref.Name}}}
			}
		}
		return nil
	}
}
