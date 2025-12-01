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
				if node, ok := obj.(*corev1.Node); !ok {
					return node.Name == nodeName
				}

				log.WithName("For").Error(nil, "Can't cast Node to *corev1.Node")
				return false
			}))).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				watchesLog := log.WithName("Watches").WithValues("type", "ConfigMap")
				cm, ok := obj.(*corev1.ConfigMap)
				if !ok {
					watchesLog.Error(nil, "Can't cast ConfigMap to *corev1.ConfigMap")
					return nil
				}
				// Only watch the agent-config ConfigMap
				if cm.Namespace != cluster.ConfigMapNamespace || cm.Name != cluster.ConfigMapName {
					watchesLog.V(4).Info("Another ConfigMap. Skip.")
					return nil
				}
				watchesLog.V(3).Info("Agent-config ConfigMap. Enqueue.")
				// Enqueue the current node
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					predicateLog := log.WithName("Predicate").WithValues("type", "ConfigMap")
					oldCM, ok1 := e.ObjectOld.(*corev1.ConfigMap)
					newCM, ok2 := e.ObjectNew.(*corev1.ConfigMap)
					if !ok1 || !ok2 {
						predicateLog.V(4).Info("Can't cast ConfigMap to *corev1.ConfigMap")
						return false
					}
					// Only watch the agent-config ConfigMap
					if newCM.Namespace != cluster.ConfigMapNamespace || newCM.Name != cluster.ConfigMapName {
						predicateLog.V(4).Info("Another ConfigMap. Skip.")
						return false
					}
					// Only enqueue if port settings changed
					predicateLog.V(3).Info("Port settings changed. Not filtering out.")
					return oldCM.Data["drbdMinPort"] != newCM.Data["drbdMinPort"] ||
						oldCM.Data["drbdMaxPort"] != newCM.Data["drbdMaxPort"]
				},
			}),
		).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				watchesLog := log.WithName("Watches").WithValues("type", "ReplicatedVolumeReplica")
				if rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
					// Only watch RVRs on the current node
					// Enqueue the current node
					if rvr.Spec.NodeName == nodeName {
						watchesLog.V(3).Info("RVR on the current node. Enqueue.")
						return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
					}
					watchesLog.V(4).Info("RVR not on the current node. Skip.")
				} else {
					watchesLog.Error(nil, "Can't cast ReplicatedVolumeReplica to *v1alpha3.ReplicatedVolumeReplica")
				}
				return nil
			}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					predicateLog := log.WithName("Predicate").WithValues("type", "Node")
					oldNode, ok1 := e.ObjectOld.(*corev1.Node)
					newNode, ok2 := e.ObjectNew.(*corev1.Node)
					if !ok1 || !ok2 {
						predicateLog.V(4).Info("Can't cast Node to *corev1.Node")
						return false
					}
					// Only watch the current node
					if newNode.Name != nodeName {
						predicateLog.V(4).Info("Node not on the current node. Skip.")
						return false
					}
					// Check if InternalIP changed
					oldIP, oldErr := getInternalIP(oldNode)
					newIP, newErr := getInternalIP(newNode)
					// If either IP is not found, consider it a change to trigger reconciliation
					if oldErr != nil || newErr != nil {
						return oldErr != nil || newErr != nil
					}
					predicateLog.V(3).Info("InternalIP changed. Not filtering out.")
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
