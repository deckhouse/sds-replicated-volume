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

// RVA is the namespace for ReplicatedVolumeAttachment-specific matchers.
var RVA rva

type rva struct{}

func asRVA(obj client.Object) *v1alpha1.ReplicatedVolumeAttachment {
	r, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedVolumeAttachment, got %T", obj))
	}
	return r
}

// OnNode matches when the attachment targets the given node.
func (rva) OnNode(name string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVA(obj)
		if r.Spec.NodeName == name {
			return true, fmt.Sprintf("on node %s", name)
		}
		return false, fmt.Sprintf("on node %s, expected %s", r.Spec.NodeName, name)
	})
}

// Custom creates a matcher with a typed function for ReplicatedVolumeAttachment.
func (rva) Custom(name string, fn func(*v1alpha1.ReplicatedVolumeAttachment) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVA(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}
