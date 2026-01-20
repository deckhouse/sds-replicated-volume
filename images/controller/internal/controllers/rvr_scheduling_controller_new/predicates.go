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

package rvrschedulingcontroller

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func RVRPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc:  func(e event.TypedCreateEvent[client.Object]) bool { return true },
			UpdateFunc:  func(e event.TypedUpdateEvent[client.Object]) bool { return false },
			DeleteFunc:  func(e event.TypedDeleteEvent[client.Object]) bool { return false },
			GenericFunc: func(e event.TypedGenericEvent[client.Object]) bool { return false },
		},
	}
}
