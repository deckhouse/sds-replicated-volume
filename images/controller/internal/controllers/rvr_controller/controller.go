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

package rvrcontroller

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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const RVRControllerName = "rvr-controller"

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()
	scheme := mgr.GetScheme()

	rec := NewReconciler(cl, scheme, mgr.GetLogger().WithName(RVRControllerName))

	return builder.ControllerManagedBy(mgr).
		Named(RVRControllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{}, builder.WithPredicates(rvrPredicates()...)).
		Owns(&snc.LVMLogicalVolume{}, builder.WithPredicates(llvPredicates()...)).
		Owns(&v1alpha1.DRBDResource{}, builder.WithPredicates(drbdrPredicates()...)).
		Watches(
			&v1alpha1.ReplicatedVolume{},
			handler.EnqueueRequestsFromMapFunc(mapRVToRVRs(cl)),
			builder.WithPredicates(rvPredicates()...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// mapRVToRVRs maps a ReplicatedVolume to all ReplicatedVolumeReplica resources that belong to it.
func mapRVToRVRs(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok || rv == nil {
			return nil
		}

		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList,
			client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvrList.Items))
		for _, rvr := range rvrList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: rvr.Name},
			})
		}
		return requests
	}
}
