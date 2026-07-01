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

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// RVR is the namespace for ReplicatedVolumeReplica-specific matchers.
var RVR rvr

type rvr struct{}

func asRVR(obj client.Object) *v1alpha1.ReplicatedVolumeReplica {
	r, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedVolumeReplica, got %T", obj))
	}
	return r
}

// OnNode matches when the replica is scheduled on the given node.
func (rvr) OnNode(name string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVR(obj)
		if r.Spec.NodeName == name {
			return true, fmt.Sprintf("on node %s", name)
		}
		return false, fmt.Sprintf("on node %s, expected %s", r.Spec.NodeName, name)
	})
}

// Type matches when the replica type equals the expected value.
func (rvr) Type(t v1alpha1.ReplicaType) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVR(obj)
		if r.Spec.Type == t {
			return true, fmt.Sprintf("type is %s", r.Spec.Type)
		}
		return false, fmt.Sprintf("type is %s, expected %s", r.Spec.Type, t)
	})
}

// Quorum matches when the replica's quorum flag equals the expected value.
// Returns false (not matched) when Quorum is nil.
func (rvr) Quorum(ok bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVR(obj)
		if r.Status.Quorum == nil {
			return false, "quorum is nil"
		}
		if *r.Status.Quorum == ok {
			return true, fmt.Sprintf("quorum is %v", *r.Status.Quorum)
		}
		return false, fmt.Sprintf("quorum is %v, expected %v", *r.Status.Quorum, ok)
	})
}

// BackingVolumeState matches when the backing volume state equals the expected value.
func (rvr) BackingVolumeState(s string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVR(obj)
		if r.Status.BackingVolume == nil {
			return false, "backing volume is nil"
		}
		actual := string(r.Status.BackingVolume.State)
		if actual == s {
			return true, fmt.Sprintf("backing volume state is %s", actual)
		}
		return false, fmt.Sprintf("backing volume state is %s, expected %s", actual, s)
	})
}

// ---------------------------------------------------------------------------
// Catalog matchers (pre-built check matchers)
// ---------------------------------------------------------------------------

// NeverLoseQuorum matches when quorum is NOT false.
// Passes when quorum is true or nil (not yet set).
func (rvr) NeverLoseQuorum() types.GomegaMatcher {
	return gomega.Not(RVR.Quorum(false))
}

// NeverCritical matches when phase is not Critical.
func (rvr) NeverCritical() types.GomegaMatcher {
	return tkmatch.PhaseNot(string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical))
}

// NeverIOSuspended matches when the Attached condition reason is not IOSuspended.
func (rvr) NeverIOSuspended() types.GomegaMatcher {
	return gomega.Not(tkmatch.ConditionReason(
		v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended,
	))
}

// NeverDegraded matches when phase is not Degraded.
func (rvr) NeverDegraded() types.GomegaMatcher {
	return tkmatch.PhaseNot(string(v1alpha1.ReplicatedVolumeReplicaPhaseDegraded))
}

// SafetyChecks returns the standard set of RVR-level safety matchers.
func (rvr) SafetyChecks() []types.GomegaMatcher {
	return []types.GomegaMatcher{
		RVR.NeverLoseQuorum(),
		RVR.NeverCritical(),
		RVR.NeverIOSuspended(),
	}
}

// StrictChecks extends SafetyChecks with NeverDegraded.
func (rvr) StrictChecks() []types.GomegaMatcher {
	return append(RVR.SafetyChecks(), RVR.NeverDegraded())
}

// Custom creates a matcher with a typed function for ReplicatedVolumeReplica.
func (rvr) Custom(name string, fn func(*v1alpha1.ReplicatedVolumeReplica) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVR(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}
