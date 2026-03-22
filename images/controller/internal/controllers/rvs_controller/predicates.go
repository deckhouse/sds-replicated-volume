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

package rvscontroller

import (
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func rvsPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}
				if !slices.Equal(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
					return true
				}
				return false
			},
		},
	}
}

func rvrsPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldObj, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
				newObj, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
				if !okOld || !okNew || oldObj == nil || newObj == nil {
					return true
				}
				return oldObj.Status.Phase != newObj.Status.Phase ||
					oldObj.Status.ReadyToUse != newObj.Status.ReadyToUse
			},
		},
	}
}
