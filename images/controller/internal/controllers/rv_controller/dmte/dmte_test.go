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

package dmte

import (
	"context"
	"iter"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

func TestDmte(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "dmte Suite")
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared types
//

// testGCtx is a minimal global context for tests.
type testGCtx struct{}

// testReplicaCtx is a minimal ReplicaCtx implementation for tests.
type testReplicaCtx struct {
	id         uint8
	name       string
	exists     bool
	generation int64
	conditions []metav1.Condition
}

func (t *testReplicaCtx) ID() uint8                      { return t.id }
func (t *testReplicaCtx) Name() string                   { return t.name }
func (t *testReplicaCtx) Exists() bool                   { return t.exists }
func (t *testReplicaCtx) Generation() int64              { return t.generation }
func (t *testReplicaCtx) Conditions() []metav1.Condition { return t.conditions }

// testCtxProvider implements ContextProvider[*testGCtx, *testReplicaCtx] for tests.
// Value type with value receivers — mirrors production usage for stenciling.
type testCtxProvider struct {
	global   *testGCtx
	replicas [32]*testReplicaCtx
}

func (c testCtxProvider) Global() *testGCtx                { return c.global }
func (c testCtxProvider) Replica(id uint8) *testReplicaCtx { return c.replicas[id] }

// testCtx builds a testCtxProvider from individual replica context entries.
func testCtx(entries ...*testReplicaCtx) testCtxProvider {
	var p testCtxProvider
	p.global = &testGCtx{}
	for _, e := range entries {
		p.replicas[e.id] = e
	}
	return p
}

// ──────────────────────────────────────────────────────────────────────────────
// Stub callbacks
//

func stubReplicaApply(*testGCtx, *testReplicaCtx) {}

func stubReplicaConfirm(*testGCtx, *testReplicaCtx, int64) ConfirmResult {
	return ConfirmResult{}
}

// neverReplicaConfirm is a confirm callback that never completes (MustConfirm is never fully confirmed).
func neverReplicaConfirm(*testGCtx, *testReplicaCtx, int64) ConfirmResult {
	return ConfirmResult{MustConfirm: ids(0, 1)}
}

func stubGlobalApply(*testGCtx) {}

func stubGlobalConfirm(*testGCtx, int64) ConfirmResult {
	return ConfirmResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Mock implementations
//

// testSlotAccessor is a minimal no-op ReplicaSlotAccessor for tests.
type testSlotAccessor struct{}

func (testSlotAccessor) GetActiveTransition(*testReplicaCtx) *Transition  { return nil }
func (testSlotAccessor) SetActiveTransition(*testReplicaCtx, *Transition) {}
func (testSlotAccessor) SetStatus(*testReplicaCtx, string, any)           {}

// mockTracker is a Tracker that records Add/Remove calls.
// By default admits all transitions; set canAdmitFn to override.
type mockTracker struct {
	added      []*Transition
	removed    []*Transition
	canAdmitFn func(*Transition) (bool, string, any)
}

func (m *mockTracker) Add(t *Transition)    { m.added = append(m.added, t) }
func (m *mockTracker) Remove(t *Transition) { m.removed = append(m.removed, t) }
func (m *mockTracker) CanAdmit(t *Transition) (bool, string, any) {
	if m.canAdmitFn != nil {
		return m.canAdmitFn(t)
	}
	return true, "", nil
}

// recordingSlotAccessor records slot operations for assertions.
type recordingSlotAccessor struct {
	activeTransitions map[uint8]*Transition // keyed by rctx.ID()
	statusMessages    map[uint8]string      // keyed by rctx.ID()
}

func newRecordingSlotAccessor() *recordingSlotAccessor {
	return &recordingSlotAccessor{
		activeTransitions: make(map[uint8]*Transition),
		statusMessages:    make(map[uint8]string),
	}
}

func (s *recordingSlotAccessor) GetActiveTransition(rctx *testReplicaCtx) *Transition {
	return s.activeTransitions[rctx.ID()]
}

func (s *recordingSlotAccessor) SetActiveTransition(rctx *testReplicaCtx, t *Transition) {
	if t == nil {
		delete(s.activeTransitions, rctx.ID())
	} else {
		s.activeTransitions[rctx.ID()] = t
	}
}

func (s *recordingSlotAccessor) SetStatus(rctx *testReplicaCtx, msg string, _ any) {
	s.statusMessages[rctx.ID()] = msg
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
//

// ids builds an IDSet from multiple IDs.
func ids(ids ...uint8) idset.IDSet {
	var s idset.IDSet
	for _, id := range ids {
		s.Add(id)
	}
	return s
}

// newTestTransition builds a minimal active transition for tests.
func newTestTransition(typ TransitionType, planID string, replicaName string) Transition {
	return Transition{
		Type:        typ,
		PlanID:      planID,
		ReplicaName: replicaName,
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Name: "step", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
		},
	}
}

// newTestReplicaPlan builds a minimal replica plan for tests.
func newTestReplicaPlan() *plan[*testGCtx, *testReplicaCtx] {
	return &plan[*testGCtx, *testReplicaCtx]{
		scope:          ReplicaScope,
		transitionType: "AddReplica",
		group:          "VotingMembership",
		displayName:    "Adding replica",
		replicaType:    v1alpha1.ReplicaTypeDiskful,
		slot:           0,
		steps: []step[*testGCtx, *testReplicaCtx]{
			{name: "✦ → D∅", scope: ReplicaScope, replicaApply: stubReplicaApply, replicaConfirm: stubReplicaConfirm},
			{name: "D∅ → D", scope: ReplicaScope, replicaApply: stubReplicaApply, replicaConfirm: stubReplicaConfirm},
		},
	}
}

// newTestGlobalPlan builds a minimal global plan for tests.
func newTestGlobalPlan() *plan[*testGCtx, *testReplicaCtx] {
	return &plan[*testGCtx, *testReplicaCtx]{
		scope:          GlobalScope,
		transitionType: "EnableMultiattach",
		group:          "Multiattach",
		displayName:    "Enabling multiattach",
		steps: []step[*testGCtx, *testReplicaCtx]{
			{name: "Enable", scope: GlobalScope, globalApply: stubGlobalApply, globalConfirm: stubGlobalConfirm},
		},
	}
}

// replicaPlan creates a minimal replica PlanBuilder via DSL for plan validation tests.
func replicaPlan(reg *Registry[*testGCtx, *testReplicaCtx], id PlanID) *PlanBuilder[*testGCtx, *testReplicaCtx] {
	rt := reg.ReplicaTransition("TestType", 0)
	return rt.Plan(id).Group("TestGroup")
}

// globalPlan creates a minimal global PlanBuilder via DSL for plan validation tests.
func globalPlan(reg *Registry[*testGCtx, *testReplicaCtx], id PlanID) *PlanBuilder[*testGCtx, *testReplicaCtx] { //nolint:unparam
	rt := reg.GlobalTransition("TestGlobalType")
	return rt.Plan(id).Group("TestGroup")
}

// singleReplicaDispatcher returns a DispatchFunc that yields a single
// DispatchReplica decision. Reduces boilerplate in dispatch tests.
func singleReplicaDispatcher(rctx *testReplicaCtx, tt TransitionType, planID PlanID) DispatchFunc[testCtxProvider] {
	return func(_ testCtxProvider) iter.Seq[DispatchDecision] {
		return func(yield func(DispatchDecision) bool) {
			yield(DispatchReplica(rctx, tt, planID))
		}
	}
}

// newTestEngine creates an Engine with a pre-configured registry for tests.
func newTestEngine(
	reg *Registry[*testGCtx, *testReplicaCtx],
	tracker Tracker,
	rev *int64,
	transitions []Transition,
	cp testCtxProvider,
) *Engine[*testGCtx, *testReplicaCtx, testCtxProvider] {
	return NewEngine(context.Background(), reg, tracker, nil, rev, transitions, cp)
}

// newTestEngineWithPlan creates an Engine with an ad-hoc plan injected directly
// into the registry (bypassing DSL). Useful for testing transition lifecycle
// without full plan registration boilerplate.
func newTestEngineWithPlan(
	p *plan[*testGCtx, *testReplicaCtx],
	planID PlanID,
	rev *int64,
	transitions []Transition, //nolint:unparam // intentionally generic for future tests
	cp testCtxProvider,
) *Engine[*testGCtx, *testReplicaCtx, testCtxProvider] {
	reg := NewRegistry[*testGCtx, *testReplicaCtx]()
	reg.plans[registryKey{transitionType: p.transitionType, planID: planID}] = p
	return NewEngine(context.Background(), reg, &mockTracker{}, nil, rev, transitions, cp)
}
