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

package worldcontroller

import (
	"context"
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/world_controller/world"
	wrld "github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

const WorldControllerName = "world-controller"

// ReconcileTarget indicates which object kind is being reconciled.
type ReconcileTarget string

const (
	// ReconcileTargetRSC is used for RSC create/delete events.
	ReconcileTargetRSC ReconcileTarget = "RSC"
	// ReconcileTargetRSCSpec is used when RSC metadata.generation changed (spec changes).
	ReconcileTargetRSCSpec ReconcileTarget = "RSCSpec"
	// ReconcileTargetRSCConfiguration is used when RSC status.configurationGeneration changed.
	ReconcileTargetRSCConfiguration ReconcileTarget = "RSCConfiguration"

	ReconcileTargetRV ReconcileTarget = "RV"

	// ReconcileTargetNodeAdd is used for Node create events.
	ReconcileTargetNodeAdd ReconcileTarget = "NodeAdd"
	// ReconcileTargetNodeDelete is used for Node delete events.
	ReconcileTargetNodeDelete ReconcileTarget = "NodeDelete"
	// ReconcileTargetNodeReady is used when Node Ready condition status changed.
	ReconcileTargetNodeReady ReconcileTarget = "NodeReady"
	// ReconcileTargetNodeUnscheduled is used when Node spec.unschedulable changed.
	ReconcileTargetNodeUnscheduled ReconcileTarget = "NodeUnscheduled"
	// ReconcileTargetNodeLabels is used when Node labels changed.
	ReconcileTargetNodeLabels ReconcileTarget = "NodeLabels"

	ReconcileTargetAgentPod ReconcileTarget = "AgentPod"
)

// Request is a custom reconcile request that includes the reconcile target.
type Request struct {
	reconcile.Request
	ReconcileTarget ReconcileTarget
}

func BuildController(mgr manager.Manager) (wrld.WorldGate, wrld.WorldBus, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger().WithName(WorldControllerName)

	w := world.NewWorld()

	rec := NewReconciler(cl, log, w)

	err := builder.TypedControllerManagedBy[Request](mgr).
		Named(WorldControllerName).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &v1alpha1.ReplicatedStorageClass{}, rscEventHandler{})).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &v1alpha1.ReplicatedVolume{}, enqueueRV())).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Node{}, nodeEventHandler{})).
		WatchesRawSource(source.TypedKind(mgr.GetCache(), &corev1.Pod{}, enqueueAgentPod(), agentPodPredicates()...)).
		WithOptions(controller.TypedOptions[Request]{MaxConcurrentReconciles: 10}).
		Complete(rec)
	if err != nil {
		return nil, nil, err
	}

	return w.GetGate(), w.GetBus(), nil
}

// rscEventHandler handles RSC events with granular reconcile targets.
type rscEventHandler struct{}

var _ handler.TypedEventHandler[*v1alpha1.ReplicatedStorageClass, Request] = rscEventHandler{}

func (rscEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*v1alpha1.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetRSC,
	})
}

func (rscEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*v1alpha1.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[Request]) {
	oldRSC := e.ObjectOld
	newRSC := e.ObjectNew
	if oldRSC == nil || newRSC == nil {
		return
	}

	key := client.ObjectKeyFromObject(newRSC)

	// metadata.generation changed (spec changes)
	if oldRSC.GetGeneration() != newRSC.GetGeneration() {
		q.Add(Request{
			Request:         reconcile.Request{NamespacedName: key},
			ReconcileTarget: ReconcileTargetRSCSpec,
		})
	}

	// status.configurationGeneration changed
	if oldRSC.Status.ConfigurationGeneration != newRSC.Status.ConfigurationGeneration {
		q.Add(Request{
			Request:         reconcile.Request{NamespacedName: key},
			ReconcileTarget: ReconcileTargetRSCConfiguration,
		})
	}
}

func (rscEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*v1alpha1.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetRSC,
	})
}

func (rscEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*v1alpha1.ReplicatedStorageClass], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetRSC,
	})
}

// nodeEventHandler handles Node events with granular reconcile targets.
type nodeEventHandler struct{}

var _ handler.TypedEventHandler[*corev1.Node, Request] = nodeEventHandler{}

func (nodeEventHandler) Create(_ context.Context, e event.TypedCreateEvent[*corev1.Node], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetNodeAdd,
	})
}

func (nodeEventHandler) Update(_ context.Context, e event.TypedUpdateEvent[*corev1.Node], q workqueue.TypedRateLimitingInterface[Request]) {
	oldNode := e.ObjectOld
	newNode := e.ObjectNew
	if oldNode == nil || newNode == nil {
		return
	}

	key := client.ObjectKeyFromObject(newNode)

	// Ready condition status changed
	if getNodeReadyStatus(oldNode) != getNodeReadyStatus(newNode) {
		q.Add(Request{
			Request:         reconcile.Request{NamespacedName: key},
			ReconcileTarget: ReconcileTargetNodeReady,
		})
	}

	// spec.unschedulable changed
	if oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable {
		q.Add(Request{
			Request:         reconcile.Request{NamespacedName: key},
			ReconcileTarget: ReconcileTargetNodeUnscheduled,
		})
	}

	// Labels changed
	if !maps.Equal(oldNode.GetLabels(), newNode.GetLabels()) {
		q.Add(Request{
			Request:         reconcile.Request{NamespacedName: key},
			ReconcileTarget: ReconcileTargetNodeLabels,
		})
	}
}

func (nodeEventHandler) Delete(_ context.Context, e event.TypedDeleteEvent[*corev1.Node], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetNodeDelete,
	})
}

func (nodeEventHandler) Generic(_ context.Context, e event.TypedGenericEvent[*corev1.Node], q workqueue.TypedRateLimitingInterface[Request]) {
	if e.Object == nil {
		return
	}
	q.Add(Request{
		Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(e.Object)},
		ReconcileTarget: ReconcileTargetNodeAdd,
	})
}

// getNodeReadyStatus returns the status of the Ready condition for a Node.
func getNodeReadyStatus(node *corev1.Node) corev1.ConditionStatus {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status
		}
	}
	return corev1.ConditionUnknown
}

// enqueueRV returns a handler that enqueues the changed RV.
func enqueueRV() handler.TypedEventHandler[*v1alpha1.ReplicatedVolume, Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, rv *v1alpha1.ReplicatedVolume) []Request {
		return []Request{{
			Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rv)},
			ReconcileTarget: ReconcileTargetRV,
		}}
	})
}

// enqueueAgentPod returns a handler that enqueues the changed agent Pod.
func enqueueAgentPod() handler.TypedEventHandler[*corev1.Pod, Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, pod *corev1.Pod) []Request {
		return []Request{{
			Request:         reconcile.Request{NamespacedName: client.ObjectKeyFromObject(pod)},
			ReconcileTarget: ReconcileTargetAgentPod,
		}}
	})
}
