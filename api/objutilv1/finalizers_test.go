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

package objutilv1_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
)

func TestFinalizersHelpers(t *testing.T) {
	obj := &metav1.PartialObjectMetadata{}

	if objutilv1.HasFinalizer(obj, "f") {
		t.Fatalf("expected no finalizer")
	}

	if changed := objutilv1.AddFinalizer(obj, "f"); !changed {
		t.Fatalf("expected changed=true on first add")
	}
	if changed := objutilv1.AddFinalizer(obj, "f"); changed {
		t.Fatalf("expected changed=false on idempotent add")
	}
	if !objutilv1.HasFinalizer(obj, "f") {
		t.Fatalf("expected finalizer to be present")
	}

	if !objutilv1.HasFinalizersOtherThan(obj, "other") {
		t.Fatalf("expected to have finalizers other than allowed")
	}
	if objutilv1.HasFinalizersOtherThan(obj, "f") {
		t.Fatalf("expected no finalizers other than allowed")
	}

	if changed := objutilv1.RemoveFinalizer(obj, "f"); !changed {
		t.Fatalf("expected changed=true on remove")
	}
	if changed := objutilv1.RemoveFinalizer(obj, "f"); changed {
		t.Fatalf("expected changed=false on repeated remove")
	}
}
