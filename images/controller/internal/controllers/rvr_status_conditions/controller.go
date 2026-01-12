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

package rvrstatusconditions

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// BuildController creates and registers the rvr-status-conditions controller with the manager.
func BuildController(mgr manager.Manager) error {
	log := mgr.GetLogger().WithName(RvrStatusConditionsControllerName)

	rec := NewReconciler(
		mgr.GetClient(),
		log.WithName("Reconciler"),
	)

	return builder.ControllerManagedBy(mgr).
		Named(RvrStatusConditionsControllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(AgentPodToRVRMapper(mgr.GetClient(), log.WithName("Mapper"))),
		).
		Complete(rec)
}

// AgentPodToRVRMapper returns a mapper function that maps agent pod events to RVR reconcile requests.
// When an agent pod changes, we need to reconcile all RVRs on the same node.
func AgentPodToRVRMapper(cl client.Client, log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return nil
		}

		// Only process agent pods
		// AgentNamespace is taken from v1alpha1.ModuleNamespace
		// Agent pods run in the same namespace as controller
		if pod.Namespace != v1alpha1.ModuleNamespace {
			return nil
		}
		if pod.Labels[AgentPodLabel] != AgentPodValue {
			return nil
		}

		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			return nil
		}

		// Find all RVRs on this node
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		if err := cl.List(ctx, &rvrList, client.MatchingFields{
			indexes.IndexFieldRVRByNodeName: nodeName,
		}); err != nil {
			log.Error(err, "Failed to list RVRs")
			return nil
		}

		requests := make([]reconcile.Request, 0, len(rvrList.Items))
		for _, rvr := range rvrList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&rvr),
			})
		}

		return requests
	}
}
