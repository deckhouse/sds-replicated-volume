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

// RV is the namespace for ReplicatedVolume-specific matchers.
var RV rv

type rv struct{}

func asRV(obj client.Object) *v1alpha1.ReplicatedVolume {
	r, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedVolume, got %T", obj))
	}
	return r
}

// Quorum matches when datamesh quorum equals the expected value.
func (rv) Quorum(q byte) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		actual := r.Status.Datamesh.Quorum
		if actual == q {
			return true, fmt.Sprintf("quorum is %d", actual)
		}
		return false, fmt.Sprintf("quorum is %d, expected %d", actual, q)
	})
}

// Members matches when the datamesh member count equals n.
func (rv) Members(n int) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		actual := len(r.Status.Datamesh.Members)
		if actual == n {
			return true, fmt.Sprintf("members: %d", actual)
		}
		return false, fmt.Sprintf("members: %d, expected %d", actual, n)
	})
}

// Multiattach matches when datamesh multiattach equals the expected value.
func (rv) Multiattach(enabled bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		actual := r.Status.Datamesh.Multiattach
		if actual == enabled {
			return true, fmt.Sprintf("multiattach is %v", actual)
		}
		return false, fmt.Sprintf("multiattach is %v, expected %v", actual, enabled)
	})
}

// HasActiveTransition matches when there is a non-completed transition
// of the given type.
func (rv) HasActiveTransition(tt string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		for i := range r.Status.DatameshTransitions {
			if string(r.Status.DatameshTransitions[i].Type) == tt && !r.Status.DatameshTransitions[i].IsCompleted() {
				return true, fmt.Sprintf("active transition %s found", tt)
			}
		}
		return false, fmt.Sprintf("no active transition %s", tt)
	})
}

// FormationComplete matches when the Formation transition is completed
// or no Formation transition exists and members are present.
func (rv) FormationComplete() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		for i := range r.Status.DatameshTransitions {
			if r.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
				if r.Status.DatameshTransitions[i].IsCompleted() {
					return true, "formation transition completed"
				}
				return false, "formation transition active"
			}
		}
		if len(r.Status.Datamesh.Members) > 0 {
			return true, "no formation transition, members present"
		}
		return false, "no formation transition, no members"
	})
}

// QuorumCorrect matches when the datamesh quorum equals
// voters/2+1. Only Diskful and LiminalDiskful are voters.
func (rv) QuorumCorrect() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		dm := &r.Status.Datamesh
		if len(dm.Members) == 0 {
			return true, "no members"
		}
		var voters int
		for i := range dm.Members {
			if dm.Members[i].Type.IsVoter() {
				voters++
			}
		}
		if voters == 0 {
			return true, "no voters"
		}
		expectedQ := byte(voters/2 + 1)
		if dm.Quorum == expectedQ {
			return true, fmt.Sprintf("quorum %d correct (voters=%d)", dm.Quorum, voters)
		}
		return false, fmt.Sprintf("quorum %d incorrect, expected %d (voters=%d)", dm.Quorum, expectedQ, voters)
	})
}

// SafetyChecks returns the standard set of RV-level check matchers.
func (rv) SafetyChecks() []types.GomegaMatcher {
	return []types.GomegaMatcher{RV.QuorumCorrect()}
}

// Custom creates a matcher with a typed function for ReplicatedVolume.
func (rv) Custom(name string, fn func(*v1alpha1.ReplicatedVolume) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRV(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}
