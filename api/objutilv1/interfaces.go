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

package objutilv1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// StatusConditionObject is a root Kubernetes object that exposes status conditions.
//
// It is intentionally small: helpers in this package need only metadata access
// (for generation/labels/finalizers/ownerRefs) and the ability to read/write
// the `.status.conditions` slice.
type StatusConditionObject interface {
	metav1.Object

	GetStatusConditions() []metav1.Condition
	SetStatusConditions([]metav1.Condition)
}

// MetaRuntimeObject is a Kubernetes object that provides both metadata (name/uid)
// and an explicit GroupVersionKind via runtime.Object.
//
// It is used for OwnerReference helpers.
type MetaRuntimeObject interface {
	metav1.Object
	runtime.Object
}
