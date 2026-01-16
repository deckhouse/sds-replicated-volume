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

// HasMatchingOwnerRef reports whether the object has an owner reference matching the given owner.
func HasMatchingOwnerRef(obj metav1.Object, owner MetaRuntimeObject, controller bool) bool {
	desired := mustDesiredOwnerRef(owner, controller)

	for _, ref := range obj.GetOwnerReferences() {
		if ownerRefsEqual(ref, desired) {
			return true
		}
	}

	return false
}

// SetOwnerRef ensures the object has an owner reference for the given owner.
// It returns whether the ownerReferences were changed.
func SetOwnerRef(obj metav1.Object, owner MetaRuntimeObject, controller bool) (changed bool) {
	desired := mustDesiredOwnerRef(owner, controller)

	refs := obj.GetOwnerReferences()

	idx := indexOfOwnerRef(refs, desired)
	if idx < 0 {
		obj.SetOwnerReferences(append(refs, desired))
		return true
	}

	if ownerRefsEqual(refs[idx], desired) {
		return false
	}

	newRefs := slices.Clone(refs)
	newRefs[idx] = desired
	obj.SetOwnerReferences(newRefs)
	return true
}

// mustDesiredOwnerRef builds an OwnerReference for the given owner.
//
// We expect owner objects passed to objutilv1 helpers to have a non-empty GVK,
// because OwnerReference requires APIVersion/Kind to be set.
// If GVK is empty, this function panics.
func mustDesiredOwnerRef(owner MetaRuntimeObject, controller bool) metav1.OwnerReference {
	gvk := owner.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		panic("objutilv1: owner object has empty GroupVersionKind; ensure APIVersion/Kind (GVK) is set on the owner runtime.Object")
	}

	if owner.GetName() == "" {
		panic("objutilv1: owner object has empty name; ensure metadata.name is set on the owner")
	}
	if owner.GetUID() == "" {
		panic("objutilv1: owner object has empty uid; ensure metadata.uid is set on the owner")
	}

	return metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       owner.GetName(),
		UID:        owner.GetUID(),
		Controller: boolPtr(controller),
	}
}

func boolPtr(v bool) *bool { return &v }

func ownerRefsEqual(a, b metav1.OwnerReference) bool {
	return a.APIVersion == b.APIVersion &&
		a.Kind == b.Kind &&
		a.Name == b.Name &&
		a.UID == b.UID &&
		boolPtrEqual(a.Controller, b.Controller)
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func indexOfOwnerRef(refs []metav1.OwnerReference, desired metav1.OwnerReference) int {
	if desired.UID != "" {
		for i := range refs {
			if refs[i].UID == desired.UID {
				return i
			}
		}
		return -1
	}

	for i := range refs {
		if refs[i].APIVersion == desired.APIVersion &&
			refs[i].Kind == desired.Kind &&
			refs[i].Name == desired.Name {
			return i
		}
	}

	return -1
}
