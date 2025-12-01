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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
)

func BuildController(mgr manager.Manager, nodeName string) error {
	const controllerName = "rvr-status-config-address-controller"

	var rec = &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName(controllerName),
	}

	return builder.ControllerManagedBy(mgr).
		Named(controllerName).
		For(
			&corev1.Node{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				if node, ok := obj.(*corev1.Node); ok {
					return node.Name == nodeName
				}
				return false
			}))).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				cm, ok := obj.(*corev1.ConfigMap)
				if !ok {
					return nil
				}
				// Only watch the agent-config ConfigMap
				if cm.Namespace != cluster.ConfigMapNamespace || cm.Name != cluster.ConfigMapName {
					return nil
				}
				// Enqueue the current node
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCM, ok1 := e.ObjectOld.(*corev1.ConfigMap)
					newCM, ok2 := e.ObjectNew.(*corev1.ConfigMap)
					if !ok1 || !ok2 {
						return false
					}
					// Only watch the agent-config ConfigMap
					if newCM.Namespace != cluster.ConfigMapNamespace || newCM.Name != cluster.ConfigMapName {
						return false
					}
					// Only enqueue if port settings changed
					return oldCM.Data["drbdMinPort"] != newCM.Data["drbdMinPort"] ||
						oldCM.Data["drbdMaxPort"] != newCM.Data["drbdMaxPort"]
				},
			}),
		).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldNode, ok1 := e.ObjectOld.(*corev1.Node)
					newNode, ok2 := e.ObjectNew.(*corev1.Node)
					if !ok1 || !ok2 {
						return false
					}
					// Only watch the current node
					if newNode.Name != nodeName {
						return false
					}
					// Check if InternalIP changed
					oldIP, oldErr := getInternalIP(oldNode)
					newIP, newErr := getInternalIP(newNode)
					// If either IP is not found, consider it a change to trigger reconciliation
					if oldErr != nil || newErr != nil {
						return oldErr != nil || newErr != nil
					}
					return oldIP != newIP
				},
			}),
		).
		Complete(rec)
}

// getInternalIP extracts the InternalIP address from a Node.
// Returns apierrors.NewNotFound if InternalIP is not found.
func getInternalIP(node *corev1.Node) (string, error) {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address, nil
		}
	}
	return "", apierrors.NewNotFound(
		corev1.Resource("nodes"),
		fmt.Sprintf("%s: InternalIP", node.Name),
	)
}
