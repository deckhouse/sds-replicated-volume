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
	"k8s.io/apimachinery/pkg/types"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
)

func TestOwnerRefsHelpers(t *testing.T) {
	obj := &metav1.PartialObjectMetadata{}

	ownerNoGVK := &metav1.PartialObjectMetadata{}
	ownerNoGVK.SetName("owner")
	ownerNoGVK.SetUID(types.UID("u1"))

	t.Run("empty_GVK_panics", func(t *testing.T) {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic when owner has empty GVK (SetOwnerRef)")
				}
			}()
			_ = objutilv1.SetOwnerRef(obj, ownerNoGVK, true)
		}()

		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic when owner has empty GVK (HasMatchingOwnerRef)")
				}
			}()
			_ = objutilv1.HasMatchingOwnerRef(obj, ownerNoGVK, true)
		}()
	})

	t.Run("empty_name_panics", func(t *testing.T) {
		owner := &metav1.PartialObjectMetadata{}
		owner.TypeMeta.APIVersion = "test.io/v1"
		owner.TypeMeta.Kind = "TestOwner"
		owner.SetUID(types.UID("u1"))

		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic when owner has empty name")
			}
		}()
		_ = objutilv1.SetOwnerRef(obj, owner, true)
	})

	t.Run("empty_uid_panics", func(t *testing.T) {
		owner := &metav1.PartialObjectMetadata{}
		owner.TypeMeta.APIVersion = "test.io/v1"
		owner.TypeMeta.Kind = "TestOwner"
		owner.SetName("owner")

		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic when owner has empty uid")
			}
		}()
		_ = objutilv1.SetOwnerRef(obj, owner, true)
	})

	owner := &metav1.PartialObjectMetadata{}
	owner.TypeMeta.APIVersion = "test.io/v1"
	owner.TypeMeta.Kind = "TestOwner"
	owner.SetName("owner")
	owner.SetUID(types.UID("u1"))

	if changed := objutilv1.SetOwnerRef(obj, owner, true); !changed {
		t.Fatalf("expected changed=true on first set")
	}
	if changed := objutilv1.SetOwnerRef(obj, owner, true); changed {
		t.Fatalf("expected changed=false on idempotent set")
	}

	if !objutilv1.HasMatchingOwnerRef(obj, owner, true) {
		t.Fatalf("expected to match ownerRef")
	}
	if objutilv1.HasMatchingOwnerRef(obj, owner, false) {
		t.Fatalf("expected not to match ownerRef with different controller flag")
	}

	// Update controller flag for the same owner UID.
	if changed := objutilv1.SetOwnerRef(obj, owner, false); !changed {
		t.Fatalf("expected changed=true when updating controller flag")
	}
	if !objutilv1.HasMatchingOwnerRef(obj, owner, false) {
		t.Fatalf("expected to match updated ownerRef")
	}
}
