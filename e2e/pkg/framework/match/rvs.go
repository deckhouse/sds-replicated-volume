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
	"strings"

	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// RVS is the namespace for ReplicatedVolumeSnapshot-specific matchers.
var RVS rvs

type rvs struct{}

func asRVS(obj client.Object) *v1alpha1.ReplicatedVolumeSnapshot {
	r, ok := obj.(*v1alpha1.ReplicatedVolumeSnapshot)
	if !ok {
		panic(fmt.Sprintf("match: expected *v1alpha1.ReplicatedVolumeSnapshot, got %T", obj))
	}
	return r
}

// PhaseIs matches when RVS.Status.Phase equals p.
func (rvs) PhaseIs(p v1alpha1.ReplicatedVolumeSnapshotPhase) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if r.Status.Phase == p {
			return true, fmt.Sprintf("phase=%s", r.Status.Phase)
		}
		return false, fmt.Sprintf("phase=%q, expected %q", r.Status.Phase, p)
	})
}

// ReadyToUse matches when RVS.Status.ReadyToUse is true.
func (rvs) ReadyToUse() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if r.Status.ReadyToUse {
			return true, "readyToUse=true"
		}
		return false, fmt.Sprintf("readyToUse=false (phase=%q)", r.Status.Phase)
	})
}

// NotReadyToUse matches when RVS.Status.ReadyToUse is false.
func (rvs) NotReadyToUse() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if !r.Status.ReadyToUse {
			return true, "readyToUse=false"
		}
		return false, "readyToUse=true"
	})
}

// PrepareComplete matches when every PrepareTransition is completed, or no
// prepare transitions exist yet AND the snapshot is already past Pending.
func (rvs) PrepareComplete() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if len(r.Status.PrepareTransitions) == 0 {
			if r.Status.Phase == v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing ||
				r.Status.Phase == v1alpha1.ReplicatedVolumeSnapshotPhaseReady {
				return true, fmt.Sprintf("no prepare transitions, phase=%s", r.Status.Phase)
			}
			return false, fmt.Sprintf("no prepare transitions yet (phase=%q)", r.Status.Phase)
		}
		for i := range r.Status.PrepareTransitions {
			if !r.Status.PrepareTransitions[i].IsCompleted() {
				return false, fmt.Sprintf("prepare transition %s active",
					r.Status.PrepareTransitions[i].Type)
			}
		}
		return true, "all prepare transitions completed"
	})
}

// SyncComplete matches when every SyncTransition is completed, or no sync
// transitions exist AND the snapshot is Ready.
func (rvs) SyncComplete() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if len(r.Status.SyncTransitions) == 0 {
			if r.Status.Phase == v1alpha1.ReplicatedVolumeSnapshotPhaseReady {
				return true, "no sync transitions, phase=Ready"
			}
			return false, fmt.Sprintf("no sync transitions yet (phase=%q)", r.Status.Phase)
		}
		for i := range r.Status.SyncTransitions {
			if !r.Status.SyncTransitions[i].IsCompleted() {
				return false, fmt.Sprintf("sync transition %s active",
					r.Status.SyncTransitions[i].Type)
			}
		}
		return true, "all sync transitions completed"
	})
}

// NoActiveTransitions matches when neither Prepare nor Sync has a
// non-completed transition.
func (rvs) NoActiveTransitions() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		for i := range r.Status.PrepareTransitions {
			if !r.Status.PrepareTransitions[i].IsCompleted() {
				return false, fmt.Sprintf("active prepare transition: %s",
					r.Status.PrepareTransitions[i].Type)
			}
		}
		for i := range r.Status.SyncTransitions {
			if !r.Status.SyncTransitions[i].IsCompleted() {
				return false, fmt.Sprintf("active sync transition: %s",
					r.Status.SyncTransitions[i].Type)
			}
		}
		return true, "no active transitions"
	})
}

// HasActivePrepareTransition matches when there is a non-completed prepare
// transition of the given type.
func (rvs) HasActivePrepareTransition(tt v1alpha1.ReplicatedVolumeDatameshTransitionType) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		for i := range r.Status.PrepareTransitions {
			if r.Status.PrepareTransitions[i].Type == tt &&
				!r.Status.PrepareTransitions[i].IsCompleted() {
				return true, fmt.Sprintf("active prepare transition %s found", tt)
			}
		}
		return false, fmt.Sprintf("no active prepare transition %s", tt)
	})
}

// HasActiveSyncTransition matches when there is a non-completed sync
// transition of the given type.
func (rvs) HasActiveSyncTransition(tt v1alpha1.ReplicatedVolumeDatameshTransitionType) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		for i := range r.Status.SyncTransitions {
			if r.Status.SyncTransitions[i].Type == tt &&
				!r.Status.SyncTransitions[i].IsCompleted() {
				return true, fmt.Sprintf("active sync transition %s found", tt)
			}
		}
		return false, fmt.Sprintf("no active sync transition %s", tt)
	})
}

// PrepareStepActive matches when the prepare transition of the given type
// has its current step with the given name and status Active.
func (rvs) PrepareStepActive(tt v1alpha1.ReplicatedVolumeDatameshTransitionType, stepName string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		return stepActiveIn(r.Status.PrepareTransitions, "prepare", tt, stepName)
	})
}

// SyncStepActive matches when the sync transition of the given type has
// its current step with the given name and status Active.
func (rvs) SyncStepActive(tt v1alpha1.ReplicatedVolumeDatameshTransitionType, stepName string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		return stepActiveIn(r.Status.SyncTransitions, "sync", tt, stepName)
	})
}

// PrepareCurrentStepMessageContains matches when the current step of the
// given prepare transition type has a Message containing substr.
func (rvs) PrepareCurrentStepMessageContains(tt v1alpha1.ReplicatedVolumeDatameshTransitionType, substr string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		return currentStepMessageContains(r.Status.PrepareTransitions, "prepare", tt, substr)
	})
}

// SyncCurrentStepMessageContains matches when the current step of the
// given sync transition type has a Message containing substr.
func (rvs) SyncCurrentStepMessageContains(tt v1alpha1.ReplicatedVolumeDatameshTransitionType, substr string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		return currentStepMessageContains(r.Status.SyncTransitions, "sync", tt, substr)
	})
}

// Members matches when len(Status.Datamesh.Members) equals n.
func (rvs) Members(n int) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		actual := len(r.Status.Datamesh.Members)
		if actual == n {
			return true, fmt.Sprintf("members: %d", actual)
		}
		return false, fmt.Sprintf("members: %d, expected %d", actual, n)
	})
}

// AllMembersReady matches when every snapshot datamesh member has ready=true.
func (rvs) AllMembersReady() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if len(r.Status.Datamesh.Members) == 0 {
			return false, "no members yet"
		}
		for i := range r.Status.Datamesh.Members {
			m := &r.Status.Datamesh.Members[i]
			if !m.Ready {
				return false, fmt.Sprintf("member %q (node=%q) not ready", m.Name, m.NodeName)
			}
		}
		return true, fmt.Sprintf("all %d members ready", len(r.Status.Datamesh.Members))
	})
}

// Custom creates a matcher with a typed function for ReplicatedVolumeSnapshot.
func (rvs) Custom(name string, fn func(*v1alpha1.ReplicatedVolumeSnapshot) bool) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		r := asRVS(obj)
		if fn(r) {
			return true, name + ": matched"
		}
		return false, name + ": not matched"
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stepActiveIn(
	ts []v1alpha1.ReplicatedVolumeDatameshTransition,
	group string,
	tt v1alpha1.ReplicatedVolumeDatameshTransitionType,
	stepName string,
) (bool, string) {
	for i := range ts {
		t := &ts[i]
		if t.Type != tt {
			continue
		}
		cur := t.CurrentStep()
		if cur == nil {
			return false, fmt.Sprintf("%s transition %s has no active step (all completed)", group, tt)
		}
		if cur.Name == stepName && cur.Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive {
			return true, fmt.Sprintf("%s step %q is active", group, stepName)
		}
		return false, fmt.Sprintf("current %s step is %q (%s), expected %q active",
			group, cur.Name, cur.Status, stepName)
	}
	return false, fmt.Sprintf("no %s transition %s found", group, tt)
}

func currentStepMessageContains(
	ts []v1alpha1.ReplicatedVolumeDatameshTransition,
	group string,
	tt v1alpha1.ReplicatedVolumeDatameshTransitionType,
	substr string,
) (bool, string) {
	for i := range ts {
		t := &ts[i]
		if t.Type != tt {
			continue
		}
		cur := t.CurrentStep()
		if cur == nil {
			return false, fmt.Sprintf("%s transition %s has no active step (all completed)", group, tt)
		}
		if strings.Contains(cur.Message, substr) {
			return true, fmt.Sprintf("%s current step %q message contains %q", group, cur.Name, substr)
		}
		return false, fmt.Sprintf("%s current step %q message %q does not contain %q",
			group, cur.Name, cur.Message, substr)
	}
	return false, fmt.Sprintf("no %s transition %s found", group, tt)
}
