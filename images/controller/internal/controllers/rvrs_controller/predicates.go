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

package rvrscontroller

import (
	"slices"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func rvrsPredicates() []predicate.Predicate {
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

func llvsPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldLLVS, okOld := e.ObjectOld.(*snc.LVMLogicalVolumeSnapshot)
				newLLVS, okNew := e.ObjectNew.(*snc.LVMLogicalVolumeSnapshot)
				if !okOld || !okNew || oldLLVS == nil || newLLVS == nil {
					return true
				}
				oldPhase := ""
				if oldLLVS.Status != nil {
					oldPhase = oldLLVS.Status.Phase
				}
				newPhase := ""
				if newLLVS.Status != nil {
					newPhase = newLLVS.Status.Phase
				}
				return oldPhase != newPhase
			},
		},
	}
}
