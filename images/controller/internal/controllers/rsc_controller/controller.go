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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

const (
	// RSCControllerName is the controller name for rsc_controller.
	RSCControllerName = "rsc-controller"
)

func BuildController(mgr manager.Manager, worldGate world.WorldGate, worldBus world.WorldBus) error {
	cl := mgr.GetClient()
	_ = worldBus // TODO: use when needed

	rec := NewReconciler(cl, worldGate)

	return builder.ControllerManagedBy(mgr).
		Named(RSCControllerName).
		For(&v1alpha1.ReplicatedStorageClass{}).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			handler.EnqueueRequestsFromMapFunc(mapRSPToRSC(cl)),
			builder.WithPredicates(RSPPredicates()...),
		).
		Watches(
			&snc.LVMVolumeGroup{},
			handler.EnqueueRequestsFromMapFunc(mapLVGToRSC(cl)),
			builder.WithPredicates(LVGPredicates()...),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(mapNodeToRSC(cl)),
			builder.WithPredicates(NodePredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedVolume{},
			rvEventHandler(),
			builder.WithPredicates(RVPredicates()...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// mapRSPToRSC maps a ReplicatedStoragePool to all ReplicatedStorageClass resources that reference it.
func mapRSPToRSC(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
		if !ok || rsp == nil {
			return nil
		}

		var rscList v1alpha1.ReplicatedStorageClassList
		if err := cl.List(ctx, &rscList, client.MatchingFields{
			indexes.IndexFieldRSCByStoragePool: rsp.Name,
		}); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rscList.Items))
		for i := range rscList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&rscList.Items[i]),
			})
		}
		return requests
	}
}

// mapLVGToRSC maps an LVMVolumeGroup to all ReplicatedStorageClass resources that reference
// a ReplicatedStoragePool containing this LVG.
func mapLVGToRSC(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		lvg, ok := obj.(*snc.LVMVolumeGroup)
		if !ok || lvg == nil {
			return nil
		}

		// Find all RSPs that reference this LVG (using index).
		var rspList v1alpha1.ReplicatedStoragePoolList
		if err := cl.List(ctx, &rspList, client.MatchingFields{
			indexes.IndexFieldRSPByLVMVolumeGroupName: lvg.Name,
		}); err != nil {
			return nil
		}

		if len(rspList.Items) == 0 {
			return nil
		}

		// Find all RSCs that reference any of the affected RSPs (using index).
		var requests []reconcile.Request
		for i := range rspList.Items {
			rspName := rspList.Items[i].Name

			var rscList v1alpha1.ReplicatedStorageClassList
			if err := cl.List(ctx, &rscList, client.MatchingFields{
				indexes.IndexFieldRSCByStoragePool: rspName,
			}); err != nil {
				continue
			}

			for j := range rscList.Items {
				requests = append(requests, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&rscList.Items[j]),
				})
			}
		}
		return requests
	}
}

// mapNodeToRSC maps a Node to all ReplicatedStorageClass resources.
// All RSCs are reconciled when relevant node properties change.
func mapNodeToRSC(cl client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		_, ok := obj.(*corev1.Node)
		if !ok {
			return nil
		}

		var rscList v1alpha1.ReplicatedStorageClassList
		if err := cl.List(ctx, &rscList); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rscList.Items))
		for i := range rscList.Items {
			rsc := &rscList.Items[i]
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(rsc),
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
