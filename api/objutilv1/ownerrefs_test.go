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

package objutilv1_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
)

func TestHasControllerRef(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	owner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner",
			Namespace: "default",
			UID:       types.UID("owner-uid"),
		},
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child",
			Namespace: "default",
		},
	}

	// Initially should return false.
	if objutilv1.HasControllerRef(obj, owner) {
		t.Fatalf("expected HasControllerRef=false before setting")
	}

	// Set controller ref.
	_, _ = objutilv1.SetControllerRef(obj, owner, scheme)

	// Now should return true.
	if !objutilv1.HasControllerRef(obj, owner) {
		t.Fatalf("expected HasControllerRef=true after setting")
	}
}

func TestHasOwnerRef(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	owner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner",
			Namespace: "default",
			UID:       types.UID("owner-uid"),
		},
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child",
			Namespace: "default",
		},
	}

	// Initially should return false.
	if objutilv1.HasOwnerRef(obj, owner) {
		t.Fatalf("expected HasOwnerRef=false before setting")
	}

	// Set owner ref (non-controller).
	_, _ = objutilv1.SetOwnerRef(obj, owner, scheme)

	// Now should return true.
	if !objutilv1.HasOwnerRef(obj, owner) {
		t.Fatalf("expected HasOwnerRef=true after setting")
	}

	// HasOwnerRef should also return true for controller refs.
	obj2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child2",
			Namespace: "default",
		},
	}
	_, _ = objutilv1.SetControllerRef(obj2, owner, scheme)
	if !objutilv1.HasOwnerRef(obj2, owner) {
		t.Fatalf("expected HasOwnerRef=true for controller ref")
	}
}

func TestSetControllerRef(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	owner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner",
			Namespace: "default",
			UID:       types.UID("owner-uid"),
		},
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child",
			Namespace: "default",
		},
	}

	// First set should return changed=true.
	changed, err := objutilv1.SetControllerRef(obj, owner, scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on first set")
	}

	// Verify owner reference was set as controller.
	if !metav1.IsControlledBy(obj, owner) {
		t.Fatalf("expected obj to be controlled by owner")
	}

	// Second set should return changed=false (idempotent).
	changed, err = objutilv1.SetControllerRef(obj, owner, scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false on idempotent set")
	}
}

func TestSetOwnerRef(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	owner := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner",
			Namespace: "default",
			UID:       types.UID("owner-uid"),
		},
	}

	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child",
			Namespace: "default",
		},
	}

	// First set should return changed=true.
	changed, err := objutilv1.SetOwnerRef(obj, owner, scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on first set")
	}

	// Verify owner reference was set (but not as controller).
	refs := obj.GetOwnerReferences()
	if len(refs) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(refs))
	}
	if refs[0].UID != owner.GetUID() {
		t.Fatalf("expected owner UID %s, got %s", owner.GetUID(), refs[0].UID)
	}
	// SetOwnerReference from controllerutil does NOT set Controller=true by default.
	if refs[0].Controller != nil && *refs[0].Controller {
		t.Fatalf("expected non-controller owner reference")
	}

	// Second set should return changed=false (idempotent).
	changed, err = objutilv1.SetOwnerRef(obj, owner, scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatalf("expected changed=false on idempotent set")
	}
}
