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

package rvrstatusconfigaddress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager, nodeName string) error {
	const controllerName = "rvr-status-config-address-controller"

	log := mgr.GetLogger().WithName(controllerName)
	var rec = &Reconciler{
		cl:  mgr.GetClient(),
		log: log,
	}

	return builder.ControllerManagedBy(mgr).
		Named(controllerName).
		For(
			&corev1.Node{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				node, ok := obj.(*corev1.Node)
				if !ok {
					log.WithName("For").Error(nil, "Can't cast Node to *corev1.Node")
					return false
				}
				return node.Name == nodeName
			}))).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(NewConfigMapEnqueueHandler(nodeName, log)),
			builder.WithPredicates(NewConfigMapUpdatePredicate(log)),
		).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(NewReplicatedVolumeReplicaEnqueueHandler(nodeName, log)),
			builder.WithPredicates(NewReplicatedVolumeReplicaUpdatePredicate(nodeName, log)),
		).
		Complete(rec)
}

// getInternalIP extracts the InternalIP address from a Node.
// Returns ErrNodeMissingInternalIP if InternalIP is not found.
func getInternalIP(node *corev1.Node) (string, error) {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNodeMissingInternalIP, node.Name)
}
