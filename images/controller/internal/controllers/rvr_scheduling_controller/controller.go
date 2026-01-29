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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const RVRSchedulingControllerName = "rvr-scheduling-controller"

func BuildController(mgr manager.Manager) error {
	r, err := NewReconciler(mgr.GetClient())
	if err != nil {
		return err
	}

	return builder.ControllerManagedBy(mgr).
		Named(RVRSchedulingControllerName).
		Watches(
			&v1alpha1.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &v1alpha1.ReplicatedVolume{}),
			builder.WithPredicates(RVRPredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			handler.EnqueueRequestsFromMapFunc(mapRSPToRV(mgr.GetClient())),
			builder.WithPredicates(RSPPredicates()...),
		).
		Complete(r)
}

// mapRSPToRV maps a ReplicatedStoragePool event to reconcile requests for
// ReplicatedVolumes that use this RSP and have at least one unscheduled non-Access replica.
func mapRSPToRV(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rsp := obj.(*v1alpha1.ReplicatedStoragePool)

		// Get RSC names directly from RSP status.
		rscNames := rsp.Status.UsedBy.ReplicatedStorageClassNames
		if len(rscNames) == 0 {
			return nil
		}

		// Find RVs that use these RSCs.
		rvNames := make(map[string]struct{})
		for _, rscName := range rscNames {
			var rvList v1alpha1.ReplicatedVolumeList
			if err := cl.List(ctx, &rvList,
				client.MatchingFields{indexes.IndexFieldRVByReplicatedStorageClassName: rscName},
				client.UnsafeDisableDeepCopy,
			); err != nil {
				continue
			}
			for i := range rvList.Items {
				rvNames[rvList.Items[i].Name] = struct{}{}
			}
		}

		if len(rvNames) == 0 {
			return nil
		}

		// Get unscheduled RVRs and filter to only those belonging to RVs using this RSP.
		var unscheduledRVRs v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &unscheduledRVRs,
			client.MatchingFields{indexes.IndexFieldRVRUnscheduled: "true"},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			return nil
		}

		// Collect unique RV names for non-Access replicas that belong to RVs using this RSP.
		result := make(map[string]struct{})
		for i := range unscheduledRVRs.Items {
			rvr := &unscheduledRVRs.Items[i]
			if rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
				continue
			}
			rvName := rvr.Spec.ReplicatedVolumeName
			if rvName == "" {
				continue
			}
			// Only include if RV uses this RSP.
			if _, ok := rvNames[rvName]; ok {
				result[rvName] = struct{}{}
			}
		}

		requests := make([]reconcile.Request, 0, len(result))
		for rvName := range result {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: rvName},
			})
		}
		return requests
	}
}
