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
	"context"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const (
	// RVControllerName is the controller name for rv-controller.
	RVControllerName = "rv-controller"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetScheme())

	return builder.ControllerManagedBy(mgr).
		Named(RVControllerName).
		For(&v1alpha1.ReplicatedVolume{}, builder.WithPredicates(rvPredicates()...)).
		Watches(
			&v1alpha1.ReplicatedStorageClass{},
			handler.EnqueueRequestsFromMapFunc(mapRSCToRVs(cl)),
			builder.WithPredicates(rscPredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedVolumeAttachment{},
			handler.EnqueueRequestsFromMapFunc(mapRVAToRV),
			builder.WithPredicates(rvaPredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(mapRVRToRV),
			builder.WithPredicates(rvrPredicates()...),
		).
		Owns(&v1alpha1.DRBDResourceOperation{}, builder.WithPredicates(drbdrOpPredicates()...)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(rec)
}

// mapRSCToRVs maps a ReplicatedStorageClass change to all ReplicatedVolume resources that reference it.
func mapRSCToRVs(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rsc, ok := obj.(*v1alpha1.ReplicatedStorageClass)
		if !ok || rsc == nil {
			return nil
		}

		var rvList v1alpha1.ReplicatedVolumeList
		if err := cl.List(ctx, &rvList,
			client.MatchingFields{indexes.IndexFieldRVByReplicatedStorageClassName: rsc.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapRSCToRVs: failed to list RVs", "rsc", rsc.Name)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvList.Items))
		for _, rv := range rvList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: rv.Name},
			})
		}
		return requests
	}
}

// mapRVAToRV maps a ReplicatedVolumeAttachment to its parent ReplicatedVolume.
func mapRVAToRV(_ context.Context, obj client.Object) []reconcile.Request {
	rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
	if !ok || rva == nil {
		return nil
	}

	if rva.Spec.ReplicatedVolumeName == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: rva.Spec.ReplicatedVolumeName},
	}}
}

// mapRVRToRV maps a ReplicatedVolumeReplica to its parent ReplicatedVolume.
func mapRVRToRV(_ context.Context, obj client.Object) []reconcile.Request {
	rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if !ok || rvr == nil {
		return nil
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName},
	}}
}
