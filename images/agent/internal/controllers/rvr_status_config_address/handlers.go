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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
)

// NewConfigMapEnqueueHandler returns a handler function that enqueues the node for reconciliation
// when the agent-config ConfigMap changes.
func NewConfigMapEnqueueHandler(nodeName string, log logr.Logger) handler.MapFunc {
	log = log.WithName("Watches").WithValues("type", "ConfigMap")
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		cm, ok := obj.(*corev1.ConfigMap)
		if !ok {
			log.Error(nil, "Can't cast ConfigMap to *corev1.ConfigMap")
			return nil
		}
		// Only watch the agent-config ConfigMap
		if cm.Namespace != cluster.ConfigMapNamespace || cm.Name != cluster.ConfigMapName {
			log.V(4).Info("Another ConfigMap. Skip.")
			return nil
		}
		log.V(3).Info("Agent-config ConfigMap. Enqueue.")
		// Enqueue the current node
		return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
	}
}

// NewConfigMapUpdatePredicate returns a predicate that filters ConfigMap update events
// to only enqueue when port settings change.
func NewConfigMapUpdatePredicate(log logr.Logger) predicate.Funcs {
	log = log.WithName("Predicate").WithValues("type", "ConfigMap")
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCM, ok1 := e.ObjectOld.(*corev1.ConfigMap)
			newCM, ok2 := e.ObjectNew.(*corev1.ConfigMap)
			if !ok1 || !ok2 {
				log.V(4).Info("Can't cast ConfigMap to *corev1.ConfigMap")
				return false
			}
			// Only watch the agent-config ConfigMap
			if newCM.Namespace != cluster.ConfigMapNamespace || newCM.Name != cluster.ConfigMapName {
				log.V(4).Info("Another ConfigMap. Skip.")
				return false
			}
			// Only enqueue if port settings changed
			log.V(3).Info("Port settings changed. Not filtering out.")
			return oldCM.Data["drbdMinPort"] != newCM.Data["drbdMinPort"] ||
				oldCM.Data["drbdMaxPort"] != newCM.Data["drbdMaxPort"]
		},
	}
}

// NewReplicatedVolumeReplicaEnqueueHandler returns a handler function that enqueues the node for reconciliation
// when a ReplicatedVolumeReplica on the current node changes.
func NewReplicatedVolumeReplicaEnqueueHandler(nodeName string, log logr.Logger) handler.MapFunc {
	log = log.WithName("Watches").WithValues("type", "ReplicatedVolumeReplica")
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica)
		if !ok {
			log.Error(nil, "Can't cast ReplicatedVolumeReplica to *v1alpha3.ReplicatedVolumeReplica")
			return nil
		}
		// Only watch RVRs on the current node
		if rvr.Spec.NodeName == nodeName {
			log.V(3).Info("RVR on the current node. Enqueue.")
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: nodeName}}}
		}
		log.V(4).Info("RVR not on the current node. Skip.")
		return nil
	}
}

// NewReplicatedVolumeReplicaUpdatePredicate returns a predicate that filters ReplicatedVolumeReplica update events
// to only enqueue when relevant fields change (e.g., NodeName, Status).
func NewReplicatedVolumeReplicaUpdatePredicate(nodeName string, log logr.Logger) predicate.Funcs {
	log = log.WithName("Predicate").WithValues("type", "ReplicatedVolumeReplica")
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldRVR, ok1 := e.ObjectOld.(*v1alpha3.ReplicatedVolumeReplica)
			newRVR, ok2 := e.ObjectNew.(*v1alpha3.ReplicatedVolumeReplica)
			if !ok1 || !ok2 {
				log.V(4).Info("Can't cast ReplicatedVolumeReplica to *v1alpha3.ReplicatedVolumeReplica")
				return false
			}
			// Only watch RVRs on the current node
			if newRVR.Spec.NodeName != nodeName {
				log.V(4).Info("RVR not on the current node. Skip.")
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
