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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// EnqueueNodeByRVR returns a event handler that enqueues the node for reconciliation
// when a ReplicatedVolumeReplica on the that node changes.
func EnqueueNodeByRVRFunc(nodeName string, log logr.Logger) handler.MapFunc {
	log = log.WithName("Watches").WithValues("type", "ReplicatedVolumeReplica")
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
		if !ok {
			log.Error(nil, "Can't cast ReplicatedVolumeReplica to *v1alpha1.ReplicatedVolumeReplica")
			return nil
		}
		// Only watch RVRs on the node
		if rvr.Spec.NodeName == nodeName {
			log.V(3).Info("RVR on the node. Enqueue.")
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
		}
		log.V(4).Info("RVR not on the node. Skip.")
		return nil
	}
}

// SkipWhenRVRNodeNameNotUpdatedPred returns a predicate that filters ReplicatedVolumeReplica update events
// to only enqueue when relevant fields change (e.g., NodeName, Status).
func SkipWhenRVRNodeNameNotUpdatedPred(log logr.Logger) predicate.Funcs {
	log = log.WithName("Predicate").WithValues("type", "ReplicatedVolumeReplica")
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRVR, ok1 := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
			newRVR, ok2 := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok1 || !ok2 {
				log.Error(nil, "Can't cast ReplicatedVolumeReplica to *v1alpha1.ReplicatedVolumeReplica")
				return false
			}
			// Enqueue if NodeName changed (shouldn't happen, but handle it)
			if oldRVR.Spec.NodeName != newRVR.Spec.NodeName {
				log.V(3).Info("RVR NodeName changed. Not filtering out.")
				return true
			}
			// Enqueue if status changed (address configuration might need update)
			log.V(3).Info("RVR status changed. Not filtering out.")
			return true
		},
	}
}

// NewNodePredicate returns a predicate function that filters Node events
// to only process the node with the specified name.
func NewNodePredicate(nodeName string, log logr.Logger) predicate.Funcs {
	log = log.WithName("Predicate").WithValues("type", "Node")
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		node, ok := obj.(*corev1.Node)
		if !ok {
			log.Error(nil, "Can't cast Node to *corev1.Node")
			return false
		}
		return node.Name == nodeName
	})
}
