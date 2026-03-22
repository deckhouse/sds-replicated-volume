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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// HasControllerRef reports whether the object is controlled by the given owner.
func HasControllerRef(obj, owner metav1.Object) bool {
	return metav1.IsControlledBy(obj, owner)
}

// HasOwnerRef reports whether the object has an owner reference (controller or not) from the given owner.
func HasOwnerRef(obj, owner metav1.Object) bool {
	return hasOwnerRefWithUID(obj, owner.GetUID())
}

// SetControllerRef ensures the object has a controller owner reference for the given owner.
// It uses scheme to determine the owner's GVK.
// It returns whether the ownerReferences were changed.
func SetControllerRef(obj, owner metav1.Object, scheme *runtime.Scheme) (changed bool, err error) {
	// Check if already controlled by this owner.
	if metav1.IsControlledBy(obj, owner) {
		return false, nil
	}

	// Use controllerutil to set the reference (it handles GVK lookup via scheme).
	if err := controllerutil.SetControllerReference(owner, obj, scheme); err != nil {
		return false, fmt.Errorf("setting controller reference: %w", err)
	}

	return true, nil
}

// SetOwnerRef ensures the object has a non-controller owner reference for the given owner.
// It uses scheme to determine the owner's GVK.
// It returns whether the ownerReferences were changed.
func SetOwnerRef(obj, owner metav1.Object, scheme *runtime.Scheme) (changed bool, err error) {
	// Check if already has owner ref with this UID.
	if hasOwnerRefWithUID(obj, owner.GetUID()) {
		return false, nil
	}

	// Use controllerutil to set the reference (it handles GVK lookup via scheme).
	if err := controllerutil.SetOwnerReference(owner, obj, scheme); err != nil {
		return false, fmt.Errorf("setting owner reference: %w", err)
	}

	return true, nil
}

// hasOwnerRefWithUID checks if obj has an owner reference with the given UID.
func hasOwnerRefWithUID(obj metav1.Object, uid types.UID) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == uid {
			return true
		}
	}
	return false
}
