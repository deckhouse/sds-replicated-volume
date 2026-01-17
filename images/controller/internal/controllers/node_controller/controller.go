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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// NodeControllerName is the controller name for node_controller.
	NodeControllerName = "node-controller"

	// singletonKey is the fixed key used for the global singleton reconcile request.
	singletonKey = "singleton"
)

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl)

	return builder.ControllerManagedBy(mgr).
		Named(NodeControllerName).
		// This controller has no primary resource of its own.
		// It watches Node and RSC events and reconciles a singleton key.
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(mapNodeToSingleton),
			builder.WithPredicates(NodePredicates()...),
		).
		Watches(
			&v1alpha1.ReplicatedStorageClass{},
			handler.EnqueueRequestsFromMapFunc(mapRSCToSingleton),
			builder.WithPredicates(RSCPredicates()...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(rec)
}

// mapNodeToSingleton maps any Node event to the singleton reconcile request.
func mapNodeToSingleton(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: singletonKey}}}
}

// mapRSCToSingleton maps any RSC event to the singleton reconcile request.
func mapRSCToSingleton(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: singletonKey}}}
}
