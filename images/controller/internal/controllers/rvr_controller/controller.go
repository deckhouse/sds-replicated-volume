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
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/world"
)

const RVRControllerName = "rvr-controller"

func BuildController(mgr manager.Manager, worldGate world.WorldGate, worldBus world.WorldBus) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetLogger().WithName(RVRControllerName), worldGate)

	return builder.ControllerManagedBy(mgr).
		Named(RVRControllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{}).
		WatchesRawSource(
			source.Channel(worldBus.NodeSource(), enqueueRVRsForNode(cl)),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}

// enqueueRVRsForNode returns a handler that maps node events to RVR reconcile requests.
// When a node event is received, it lists all RVRs on that node and enqueues them.
func enqueueRVRsForNode(cl client.Client) handler.TypedEventHandler[world.NodeName, reconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, nodeName world.NodeName) []reconcile.Request {
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList, client.MatchingFields{
			indexes.IndexFieldRVRByNodeName: string(nodeName),
		}); err != nil {
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvrList.Items))
		for i := range rvrList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&rvrList.Items[i]),
			})
		}
		return requests
	})
}
