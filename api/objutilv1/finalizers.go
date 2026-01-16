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

package objutilv1

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HasFinalizer reports whether the object has the given finalizer.
func HasFinalizer(obj metav1.Object, finalizer string) bool {
	return slices.Contains(obj.GetFinalizers(), finalizer)
}

// AddFinalizer ensures the given finalizer is present on the object.
// It returns whether the finalizers were changed.
func AddFinalizer(obj metav1.Object, finalizer string) (changed bool) {
	finalizers := obj.GetFinalizers()
	if slices.Contains(finalizers, finalizer) {
		return false
	}

	obj.SetFinalizers(append(finalizers, finalizer))
	return true
}

// RemoveFinalizer removes the given finalizer from the object.
// It returns whether the finalizers were changed.
func RemoveFinalizer(obj metav1.Object, finalizer string) (changed bool) {
	finalizers := obj.GetFinalizers()

	idx := slices.Index(finalizers, finalizer)
	if idx < 0 {
		return false
	}

	obj.SetFinalizers(slices.Delete(finalizers, idx, idx+1))
	return true
}

// HasFinalizersOtherThan reports whether the object has any finalizers not in the allowed list.
func HasFinalizersOtherThan(obj metav1.Object, allowedFinalizers ...string) bool {
	finalizers := obj.GetFinalizers()

	switch len(allowedFinalizers) {
	case 0:
		return len(finalizers) > 0
	case 1:
		allowed := allowedFinalizers[0]
		for _, f := range finalizers {
			if f != allowed {
				return true
			}
		}
		return false
	default:
		for _, f := range finalizers {
			if !slices.Contains(allowedFinalizers, f) {
				return true
			}
		}
		return false
	}
}
