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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
)

func TestAnnotationsHelpers(t *testing.T) {
	obj := &metav1.PartialObjectMetadata{}

	if objutilv1.HasAnnotation(obj, "k") {
		t.Fatalf("expected no annotation")
	}
	if objutilv1.HasAnnotationValue(obj, "k", "v") {
		t.Fatalf("expected no annotation value")
	}

	if changed := objutilv1.SetAnnotation(obj, "k", "v"); !changed {
		t.Fatalf("expected changed=true on first set")
	}
	if !objutilv1.HasAnnotation(obj, "k") {
		t.Fatalf("expected annotation to be present")
	}
	if !objutilv1.HasAnnotationValue(obj, "k", "v") {
		t.Fatalf("expected annotation value to match")
	}

	if changed := objutilv1.SetAnnotation(obj, "k", "v"); changed {
		t.Fatalf("expected changed=false on idempotent set")
	}

	if changed := objutilv1.RemoveAnnotation(obj, "k"); !changed {
		t.Fatalf("expected changed=true on remove")
	}
	if changed := objutilv1.RemoveAnnotation(obj, "k"); changed {
		t.Fatalf("expected changed=false on repeated remove")
	}
}

func TestSetAnnotation_EmptyStringOnNilAnnotations(t *testing.T) {
	obj := &metav1.PartialObjectMetadata{}

	if changed := objutilv1.SetAnnotation(obj, "k", ""); !changed {
		t.Fatalf("expected changed=true when setting empty-string annotation on nil annotations")
	}
	if !objutilv1.HasAnnotation(obj, "k") {
		t.Fatalf("expected annotation key to be present after SetAnnotation with empty value")
	}
	if !objutilv1.HasAnnotationValue(obj, "k", "") {
		t.Fatalf("expected annotation value to be empty string")
	}

	if changed := objutilv1.SetAnnotation(obj, "k", ""); changed {
		t.Fatalf("expected changed=false on idempotent set of empty-string annotation")
	}
}
