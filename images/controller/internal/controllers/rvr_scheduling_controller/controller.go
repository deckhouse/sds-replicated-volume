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

package rvrschedulingcontroller

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
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
)

// RVRSchedulingControllerName is the controller name for rvr-scheduling-controller.
const RVRSchedulingControllerName = "rvr-scheduling-controller"

func BuildController(mgr manager.Manager, schedulerExtenderURL string) error {
	cl := mgr.GetClient()

	extenderClient, err := schext.NewClient(schedulerExtenderURL)
	if err != nil {
		return err
	}

	r := NewReconciler(
		cl,
		mgr.GetLogger().WithName(RVRSchedulingControllerName),
		mgr.GetScheme(),
		extenderClient,
	)

	return builder.ControllerManagedBy(mgr).
		Named(RVRSchedulingControllerName).
		For(&v1alpha1.ReplicatedVolume{}, builder.WithPredicates(RVPredicates()...)).
		Watches(
			&v1alpha1.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(mapRVRToRV),
			builder.WithPredicates(RVRPredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			handler.EnqueueRequestsFromMapFunc(mapRSPToRV(cl)),
			builder.WithPredicates(RSPPredicates()...),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(r)
}

// mapRVRToRV maps a ReplicatedVolumeReplica event to a reconcile request
// for the parent ReplicatedVolume referenced by spec.replicatedVolumeName.
func mapRVRToRV(_ context.Context, obj client.Object) []reconcile.Request {
	rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if !ok || rvr == nil || rvr.Spec.ReplicatedVolumeName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}}}
}

// mapRSPToRV maps a ReplicatedStoragePool event to reconcile requests for all
// ReplicatedVolume objects that reference it via status.configuration.replicatedStoragePoolName.
func mapRSPToRV(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
		if !ok || rsp == nil {
			return nil
		}

		var rvList v1alpha1.ReplicatedVolumeList
		if err := cl.List(ctx, &rvList,
			client.MatchingFields{indexes.IndexFieldRVByStoragePoolName: rsp.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapRSPToRV: failed to list RVs", "rsp", rsp.Name)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvList.Items))
		for i := range rvList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: rvList.Items[i].Name},
			})
		}
		return requests
	}
}
