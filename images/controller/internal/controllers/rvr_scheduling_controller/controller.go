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
		rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
		if !ok || rsp == nil {
			return nil
		}

		// Get RSC names directly from RSP status.
		rscNames := rsp.Status.UsedBy.ReplicatedStorageClassNames
		if len(rscNames) == 0 {
			return nil
		}

		// Build set of RSC names for quick lookup.
		rscNamesSet := make(map[string]struct{}, len(rscNames))
		for _, rscName := range rscNames {
			rscNamesSet[rscName] = struct{}{}
		}

		// Get all unscheduled non-Access RVRs and collect unique RV names.
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList,
			client.MatchingFields{indexes.IndexFieldRVRUnscheduledNonAccess: indexes.IndexValueRVRUnscheduledNonAccess()},
			client.UnsafeDisableDeepCopy,
		); err != nil || len(rvrList.Items) == 0 {
			return nil
		}

		// Collect unique RV names from unscheduled RVRs.
		rvNamesWithUnscheduled := make(map[string]struct{}, len(rvrList.Items))
		for i := range rvrList.Items {
			rvNamesWithUnscheduled[rvrList.Items[i].Spec.ReplicatedVolumeName] = struct{}{}
		}

		// For each unique RV with unscheduled RVRs, check if it uses one of the RSCs from this RSP.
		var requests []reconcile.Request
		for rvName := range rvNamesWithUnscheduled {
			var rv v1alpha1.ReplicatedVolume
			if err := cl.Get(ctx, client.ObjectKey{Name: rvName}, &rv); err != nil {
				continue
			}
			if _, ok := rscNamesSet[rv.Spec.ReplicatedStorageClassName]; ok {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKey{Name: rvName},
				})
			}
		}
		return requests
	}
}
