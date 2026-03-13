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

package nodecontroller

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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/controlleroptions"
)

// NodeControllerName is the controller name for node_controller.
const NodeControllerName = "node-controller"

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl)

	return builder.ControllerManagedBy(mgr).
		Named(NodeControllerName).
		// This controller reconciles individual Node objects.
		// It also watches RSP and DRBDResource events.
		For(&corev1.Node{}, builder.WithPredicates(nodePredicates()...)).
		Watches(
			&v1alpha1.ReplicatedStoragePool{},
			rspEventHandler(),
			builder.WithPredicates(rspPredicates()...),
		).
		Watches(
			&v1alpha1.DRBDResource{},
			handler.EnqueueRequestsFromMapFunc(mapDRBDResourceToNode),
			builder.WithPredicates(drbdResourcePredicates()...),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter(),
		}).
		Complete(rec)
}

// rspEventHandler returns an event handler for RSP that computes the delta of eligibleNodes
// and enqueues reconcile requests for affected nodes.
func rspEventHandler() handler.TypedEventHandler[client.Object, reconcile.Request] {
	return handler.TypedFuncs[client.Object, reconcile.Request]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			rsp, ok := e.Object.(*v1alpha1.ReplicatedStoragePool)
			if !ok || rsp == nil {
				return
			}
			enqueueNodesFromRSP(q, rsp)
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
			newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
			if !okOld || !okNew || oldRSP == nil || newRSP == nil {
				return
			}
			// Compute delta: nodes added or removed from eligibleNodes.
			enqueueEligibleNodesDelta(q, oldRSP.Status.EligibleNodes, newRSP.Status.EligibleNodes)
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			rsp, ok := e.Object.(*v1alpha1.ReplicatedStoragePool)
			if !ok || rsp == nil {
				return
			}
			enqueueNodesFromRSP(q, rsp)
		},
	}
}

// enqueueNodesFromRSP enqueues reconcile requests for all nodes in RSP's eligibleNodes.
func enqueueNodesFromRSP(q workqueue.TypedRateLimitingInterface[reconcile.Request], rsp *v1alpha1.ReplicatedStoragePool) {
	for i := range rsp.Status.EligibleNodes {
		nodeName := rsp.Status.EligibleNodes[i].NodeName
		if nodeName != "" {
			q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: nodeName}})
		}
	}
}

// enqueueEligibleNodesDelta enqueues reconcile requests for nodes that were added or removed.
//
// Uses EligibleNodesSortedIndex to handle potentially unsorted input safely.
// In the common case (already sorted), this adds only O(n) overhead with zero allocations.
func enqueueEligibleNodesDelta(
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
	oldNodes, newNodes []v1alpha1.ReplicatedStoragePoolEligibleNode,
) {
	// Build sorted indices — O(n) if already sorted, O(n log n) otherwise.
	oldIdx := v1alpha1.NewEligibleNodesSortedIndex(oldNodes)
	newIdx := v1alpha1.NewEligibleNodesSortedIndex(newNodes)

	// Merge-style traversal of two sorted lists to find delta.
	i, j := 0, 0
	for i < oldIdx.Len() || j < newIdx.Len() {
		switch {
		case i >= oldIdx.Len():
			// Remaining newNodes are all added.
			if name := newIdx.NodeName(j); name != "" {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
			}
			j++
		case j >= newIdx.Len():
			// Remaining oldNodes are all removed.
			if name := oldIdx.NodeName(i); name != "" {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
			}
			i++
		case oldIdx.NodeName(i) < newIdx.NodeName(j):
			// Node was removed.
			if name := oldIdx.NodeName(i); name != "" {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
			}
			i++
		case oldIdx.NodeName(i) > newIdx.NodeName(j):
			// Node was added.
			if name := newIdx.NodeName(j); name != "" {
				q.Add(reconcile.Request{NamespacedName: client.ObjectKey{Name: name}})
			}
			j++
		default:
			// Same node in both lists, no change.
			i++
			j++
		}
	}
}

// mapDRBDResourceToNode maps a DRBDResource event to a reconcile request for the node it belongs to.
func mapDRBDResourceToNode(_ context.Context, obj client.Object) []reconcile.Request {
	dr, ok := obj.(*v1alpha1.DRBDResource)
	if !ok || dr == nil {
		return nil
	}
	if dr.Spec.NodeName == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: dr.Spec.NodeName}}}
}
