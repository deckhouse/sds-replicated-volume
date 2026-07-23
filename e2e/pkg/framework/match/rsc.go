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

package match

import (
	"fmt"

	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// RSC is the namespace for ReplicatedStorageClass-specific matchers.
var RSC rsc

type rsc struct{}

func asRSC(obj client.Object) *v1alpha1.ReplicatedStorageClass {
	r, ok := obj.(*v1alpha1.ReplicatedStorageClass)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedStorageClass, got %T", obj))
	}
	return r
}

// VolumesAligned matches when status.volumes.aligned equals n.
// A nil counter is treated as 0.
func (rsc) VolumesAligned(n int32) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRSC(obj)
		var actual int32
		if a := r.Status.Volumes.Aligned; a != nil {
			actual = *a
		}
		if actual == n {
			return true, fmt.Sprintf("volumes.aligned is %d", actual)
		}
		return false, fmt.Sprintf("volumes.aligned is %d, expected %d", actual, n)
	})
}

// VolumesStale matches when status.volumes.staleConfiguration equals n.
// A nil counter is treated as 0.
func (rsc) VolumesStale(n int32) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRSC(obj)
		var actual int32
		if s := r.Status.Volumes.StaleConfiguration; s != nil {
			actual = *s
		}
		if actual == n {
			return true, fmt.Sprintf("volumes.staleConfiguration is %d", actual)
		}
		return false, fmt.Sprintf("volumes.staleConfiguration is %d, expected %d", actual, n)
	})
}

// Custom creates a matcher with a typed function for ReplicatedStorageClass.
func (rsc) Custom(name string, fn func(*v1alpha1.ReplicatedStorageClass) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRSC(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}
