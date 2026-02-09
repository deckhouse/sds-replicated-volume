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

package rsccontroller

import (
	"context"
	"os"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const (
	// RSCControllerName is the controller name for rsc_controller.
	RSCControllerName = "rsc-controller"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	ns := controllerNamespaceDefault
	if v := os.Getenv(podNamespaceEnvVar); v != "" {
		ns = v
	}

	rec := NewReconciler(cl, ns)

	return builder.ControllerManagedBy(mgr).
		Named(RSCControllerName).
		For(&v1alpha1.ReplicatedStorageClass{}).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			handler.EnqueueRequestsFromMapFunc(mapRSPToRSC(cl)),
			builder.WithPredicates(rspPredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedVolume{},
			rvEventHandler(),
			builder.WithPredicates(rvPredicates()...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// mapRSPToRSC maps a ReplicatedStoragePool to all ReplicatedStorageClass resources that reference it.
// It queries RSCs using two indexes:
//   - spec.storagePool (for migration from deprecated field)
//   - status.storagePoolName (for auto-generated RSPs)
func mapRSPToRSC(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
		if !ok || rsp == nil {
			return nil
		}

		// Deduplicate RSC names from both indexes.
		seen := make(map[string]struct{})

		// Query by spec.storagePool (migration).
		var listBySpec v1alpha1.ReplicatedStorageClassList
		if err := cl.List(ctx, &listBySpec,
			client.MatchingFields{indexes.IndexFieldRSCByStoragePool: rsp.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapRSPToRSC: failed to list RSCs by spec.storagePool", "rsp", rsp.Name)
		} else {
			for i := range listBySpec.Items {
				seen[listBySpec.Items[i].Name] = struct{}{}
			}
		}

		// Query by status.storagePoolName (auto-generated).
		var listByStatus v1alpha1.ReplicatedStorageClassList
		if err := cl.List(ctx, &listByStatus,
			client.MatchingFields{indexes.IndexFieldRSCByStatusStoragePoolName: rsp.Name},
			client.UnsafeDisableDeepCopy,
		); err != nil {
			log.FromContext(ctx).Error(err, "mapRSPToRSC: failed to list RSCs by status.storagePoolName", "rsp", rsp.Name)
		} else {
			for i := range listByStatus.Items {
				seen[listByStatus.Items[i].Name] = struct{}{}
			}
		}

		// Also enqueue RSCs from usedBy (handles orphaned entries for deleted RSCs).
		for _, rscName := range rsp.Status.UsedBy.ReplicatedStorageClassNames {
			seen[rscName] = struct{}{}
		}

		if len(seen) == 0 {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(seen))
		for name := range seen {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: name},
			})
		}
		return requests
	}
}

// rvEventHandler returns an event handler for ReplicatedVolume events.
// On Update, it enqueues both old and new storage classes if they differ.
func rvEventHandler() handler.TypedEventHandler[client.Object, reconcile.Request] {
	enqueueRSC := func(q workqueue.TypedRateLimitingInterface[reconcile.Request], rscName string) {
		if rscName != "" {
			q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: rscName}})
		}
	}

	return handler.TypedFuncs[client.Object, reconcile.Request]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			rv, ok := e.Object.(*v1alpha1.ReplicatedVolume)
			if !ok || rv == nil {
				return
			}
			enqueueRSC(q, rv.Spec.ReplicatedStorageClassName)
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
			newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
			if !okOld || !okNew || oldRV == nil || newRV == nil {
				return
			}
			// Enqueue both old and new storage classes (deduplication happens in workqueue).
			enqueueRSC(q, oldRV.Spec.ReplicatedStorageClassName)
			enqueueRSC(q, newRV.Spec.ReplicatedStorageClassName)
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			rv, ok := e.Object.(*v1alpha1.ReplicatedVolume)
			if !ok || rv == nil {
				return
			}
			enqueueRSC(q, rv.Spec.ReplicatedStorageClassName)
		},
	}
}
