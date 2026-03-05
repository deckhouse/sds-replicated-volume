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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("NewEngine", func() {
	It("panics on nil registry", func() {
		var rev int64
		var transitions []Transition
		Expect(func() {
			NewEngine[*testGCtx, *testReplicaCtx, testCtxProvider](context.Background(), nil, &mockTracker{}, nil, &rev, transitions, testCtx())
		}).To(Panic())
	})

	It("panics on nil tracker", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		var transitions []Transition
		Expect(func() {
			NewEngine(context.Background(), reg, nil, nil, &rev, transitions, testCtx())
		}).To(Panic())
	})

	It("panics on nil revision", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var transitions []Transition
		Expect(func() {
			NewEngine(context.Background(), reg, &mockTracker{}, nil, nil, transitions, testCtx())
		}).To(Panic())
	})

	It("accepts nil transitions slice", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())
		Expect(e).NotTo(BeNil())
	})

	It("initializes tracker from existing transitions", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		tracker := &mockTracker{}
		var rev int64
		transitions := []Transition{
			newTestTransition("AddReplica", "access/v1", "rv-1-0"),
			newTestTransition("EnableMultiattach", "enable/v1", ""),
		}

		newTestEngine(reg, tracker, &rev, transitions, testCtx())

		Expect(tracker.added).To(HaveLen(2))
	})

	It("initializes slot pointers from existing transitions with known plans", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("T").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		tracker := &mockTracker{}
		var rev int64
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-3")}

		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}
		newTestEngine(reg, tracker, &rev, transitions, testCtx(rctx3))

		Expect(slot.activeTransitions).To(HaveKey(uint8(3)))
	})

	It("skips slot init for unknown plan", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// No plan registered — transition has an unknown plan.
		tracker := &mockTracker{}
		var rev int64
		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}
		transitions := []Transition{newTestTransition("AddReplica", "unknown/v1", "rv-1-3")}

		newTestEngine(reg, tracker, &rev, transitions, testCtx(rctx3))

		// Tracker should still have the transition, but slot should NOT be set.
		Expect(tracker.added).To(HaveLen(1))
		Expect(slot.activeTransitions).To(BeEmpty())
	})

	It("skips slot init when replica context not found", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("T").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		tracker := &mockTracker{}
		var rev int64
		// Transition references rv-1-3, but no replica context for id 3 in provider.
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-3")}

		newTestEngine(reg, tracker, &rev, transitions, testCtx()) // no replicas

		Expect(tracker.added).To(HaveLen(1))
		Expect(slot.activeTransitions).To(BeEmpty())
	})

})

var _ = Describe("Process", func() {
	It("returns false with no transitions and no dispatchers", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		var transitions []Transition
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx())

		Expect(e.Process(context.Background())).To(BeFalse())
	})

	It("settles a single-step transition that is already confirmed", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Plan with auto-confirming step (empty MustConfirm == empty Confirmed).
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).Build()

		tracker := &mockTracker{}
		var rev int64
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-0")}
		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		e := newTestEngine(reg, tracker, &rev, transitions, testCtx(rctx0))

		changed := e.Process(context.Background())

		// Transition should be completed and removed.
		Expect(changed).To(BeTrue())
		Expect(e.Finalize()).To(BeEmpty())
	})

	It("dispatches a new transition via dispatcher", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).Build()

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}

		tracker := &mockTracker{}
		var rev int64
		var transitions []Transition
		e := NewEngine(context.Background(), reg, tracker, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, transitions, testCtx(rctx5))

		changed := e.Process(context.Background())

		Expect(changed).To(BeTrue())
		result := e.Finalize()
		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("AddReplica")))
		Expect(result[0].ReplicaName).To(Equal("rv-1-5"))
		Expect(rev).To(Equal(int64(1)))
		// Slot should have the active transition and a status message.
		Expect(slot.activeTransitions).To(HaveKey(uint8(5)))
		Expect(slot.statusMessages[5]).To(ContainSubstring("Joining"))
	})

	It("dispatch blocked by guard writes message to slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Guards(func(*testGCtx, *testReplicaCtx) GuardResult {
				return GuardResult{Blocked: true, Message: "node not eligible"}
			}).
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}

		var rev int64
		var transitions []Transition
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, transitions, testCtx(rctx5))

		changed := e.Process(context.Background())

		Expect(changed).To(BeFalse())
		Expect(e.Finalize()).To(BeEmpty())
		Expect(slot.statusMessages[5]).To(Equal("node not eligible"))
	})

	It("dispatch blocked by tracker writes message to slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}

		blockingTracker := &mockTracker{}
		blockingTracker.canAdmitFn = func(_ *Transition) (bool, string, any) {
			return false, "blocked by quorum", nil
		}

		var rev int64
		var transitions []Transition
		e := NewEngine(context.Background(), reg, blockingTracker, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, transitions, testCtx(rctx5))

		changed := e.Process(context.Background())

		Expect(changed).To(BeFalse())
		Expect(e.Finalize()).To(BeEmpty())
		Expect(slot.statusMessages[5]).To(Equal("blocked by quorum"))
	})

	It("dispatch slot conflict with unknown active plan uses empty display name", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Register only the NEW plan; the active transition has an unknown plan.
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		// Pre-existing transition with an unknown plan occupies the slot.
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		unknownTransition := newTestTransition("OldType", "old/v1", "rv-1-5")
		slot.SetActiveTransition(rctx5, &unknownTransition)

		var rev int64
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, nil, testCtx(rctx5))

		e.Process(context.Background())

		// Should still write a blocked message, with empty active plan name.
		Expect(slot.statusMessages[5]).To(ContainSubstring("waiting for"))
		Expect(slot.statusMessages[5]).To(ContainSubstring("Joining"))
	})

	It("dispatch slot conflict writes blocked-by-active message", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Plan with a confirm that never completes (step stays active).
		neverConfirm := func(_ *testGCtx, _ *testReplicaCtx, _ int64) ConfirmResult {
			return ConfirmResult{MustConfirm: ids(0, 1), Confirmed: ids(0)}
		}
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, neverConfirm)).Build()

		// Pre-existing transition occupies the slot for replica 5.
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-5")}

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}

		var rev int64
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, transitions, testCtx(rctx5))

		changed := e.Process(context.Background())

		// Settle updates the step message → changed is true.
		Expect(changed).To(BeTrue())
		Expect(slot.statusMessages[5]).To(ContainSubstring("waiting for"))
		Expect(slot.statusMessages[5]).To(ContainSubstring("Joining"))
	})

	It("no-dispatch decision writes message to slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		dispatcher := func(_ testCtxProvider) iter.Seq[DispatchDecision] {
			return func(yield func(DispatchDecision) bool) {
				yield(NoDispatch(rctx5, 0, "Volume is attached", "Attached"))
			}
		}

		var rev int64
		var transitions []Transition
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{dispatcher}, &rev, transitions, testCtx(rctx5))

		changed := e.Process(context.Background())

		Expect(changed).To(BeFalse())
		Expect(slot.statusMessages[5]).To(Equal("Volume is attached"))
	})

	It("multi-step transition settles across multiple iterations", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		// Both steps auto-confirm (empty MustConfirm).
		reg.ReplicaTransition("AddReplica", 0).
			Plan("diskful/v1").Group("G").DisplayName("Adding").
			Steps(
				ReplicaStep("✦ → D∅", stubReplicaApply, stubReplicaConfirm),
				ReplicaStep("D∅ → D", stubReplicaApply, stubReplicaConfirm),
			).Build()

		now := metav1.Now()
		transitions := []Transition{{
			Type:        "AddReplica",
			PlanID:      "diskful/v1",
			ReplicaName: "rv-1-0",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &now},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}}

		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx(rctx0))

		changed := e.Process(context.Background())

		// Both steps auto-confirmed → transition completed and removed.
		Expect(changed).To(BeTrue())
		Expect(e.Finalize()).To(BeEmpty())
	})

	It("onComplete callback is called when transition completes", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		completeCalled := false
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).
			OnComplete(func(*testGCtx, *testReplicaCtx) { completeCalled = true }).Build()

		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-0")}
		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx(rctx0))

		e.Process(context.Background())

		Expect(completeCalled).To(BeTrue())
		Expect(e.Finalize()).To(BeEmpty())
	})

	It("cancelActiveOnCreate cancels active transition before creating new one", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Normal plan (non-cancelling, never confirms).
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, neverReplicaConfirm)).Build()

		// Emergency plan with CancelActiveOnCreate (also never confirms to stay visible).
		reg.ReplicaTransition("ForceRemove", 0).
			Plan("force/v1").Group("Emergency").DisplayName("Force removing").
			CancelActiveOnCreate(true).
			Steps(ReplicaStep("remove", stubReplicaApply, neverReplicaConfirm)).Build()

		// Pre-existing normal transition.
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-3")}

		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}

		var rev int64
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx3, "ForceRemove", "force/v1")}, &rev, transitions, testCtx(rctx3))

		changed := e.Process(context.Background())

		Expect(changed).To(BeTrue())
		// The old AddReplica is cancelled, ForceRemove is created (stays because neverConfirm).
		result := e.Finalize()
		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("ForceRemove")))
	})

	It("unknown transitions are skipped during settle", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		transitions := []Transition{newTestTransition("UnknownType", "unknown/v1", "rv-1-0")}
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx())

		changed := e.Process(context.Background())

		Expect(changed).To(BeFalse())
		Expect(e.Finalize()).To(HaveLen(1)) // left in place
	})

	It("global transition settles to completion", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		completeCalled := false
		reg.GlobalTransition("EnableMultiattach").
			Plan("enable/v1").Group("Multiattach").DisplayName("Enabling multiattach").
			Steps(GlobalStep("Enable", stubGlobalApply, stubGlobalConfirm)).
			OnComplete(func(*testGCtx) { completeCalled = true }).Build()

		// Pre-existing global transition (auto-confirming step).
		now := metav1.Now()
		transitions := []Transition{{
			Type:   "EnableMultiattach",
			PlanID: "enable/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "Enable", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &now},
			},
		}}

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx())

		changed := e.Process(context.Background())

		Expect(changed).To(BeTrue())
		Expect(completeCalled).To(BeTrue())
		Expect(e.Finalize()).To(BeEmpty())
	})

	It("global plan dispatches without slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		reg.GlobalTransition("EnableMultiattach").
			Plan("enable/v1").Group("Multiattach").DisplayName("Enabling multiattach").
			Steps(GlobalStep("Enable", stubGlobalApply, stubGlobalConfirm)).Build()

		dispatcher := func(_ testCtxProvider) iter.Seq[DispatchDecision] {
			return func(yield func(DispatchDecision) bool) {
				yield(DispatchGlobal("EnableMultiattach", "enable/v1"))
			}
		}

		var rev int64
		var transitions []Transition
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{dispatcher}, &rev, transitions, testCtx())

		changed := e.Process(context.Background())

		// Transition created by dispatch; settles on next Process() call.
		Expect(changed).To(BeTrue())
		result := e.Finalize()
		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("EnableMultiattach")))
		Expect(rev).To(Equal(int64(1)))
	})
})

var _ = Describe("CreateReplicaTransition", func() {
	It("creates a transition through the full pipeline", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).Build()

		var rev int64
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx(rctx5))

		t, reason := e.CreateReplicaTransition("AddReplica", "access/v1", 5)

		Expect(reason).To(BeEmpty())
		Expect(t).NotTo(BeNil())
		Expect(t.Type).To(Equal(TransitionType("AddReplica")))
		Expect(t.ReplicaName).To(Equal("rv-1-5"))
		Expect(rev).To(Equal(int64(1)))
		Expect(slot.activeTransitions).To(HaveKey(uint8(5)))
		Expect(slot.statusMessages[5]).To(ContainSubstring("Joining"))
	})

	It("blocks when guard fails", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(0, newRecordingSlotAccessor())

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Guards(func(*testGCtx, *testReplicaCtx) GuardResult {
				return GuardResult{Blocked: true, Message: "not ready"}
			}).
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		var rev int64
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx(rctx5))

		t, reason := e.CreateReplicaTransition("AddReplica", "access/v1", 5)

		Expect(t).To(BeNil())
		Expect(reason).To(Equal("not ready"))
	})

	It("blocks when tracker denies admission", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(0, newRecordingSlotAccessor())

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		tracker := &mockTracker{canAdmitFn: func(_ *Transition) (bool, string, any) {
			return false, "blocked by quorum", nil
		}}

		var rev int64
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		e := newTestEngine(reg, tracker, &rev, nil, testCtx(rctx5))

		t, reason := e.CreateReplicaTransition("AddReplica", "access/v1", 5)

		Expect(t).To(BeNil())
		Expect(reason).To(Equal("blocked by quorum"))
	})

	It("blocks when slot is occupied", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, neverReplicaConfirm)).Build()

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		var rev int64
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-5")}
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx(rctx5))

		t, reason := e.CreateReplicaTransition("AddReplica", "access/v1", 5)

		Expect(t).To(BeNil())
		Expect(reason).To(ContainSubstring("waiting for"))
	})

	It("panics on unknown plan", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx(&testReplicaCtx{id: 0, name: "rv-1-0"}))

		Expect(func() { e.CreateReplicaTransition("Unknown", "x/v1", 0) }).To(Panic())
	})

	It("panics on unknown replica ID", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(0, newRecordingSlotAccessor())

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx()) // no replicas in provider

		Expect(func() { e.CreateReplicaTransition("AddReplica", "access/v1", 99) }).To(Panic())
	})

	It("panics when already finalized", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.ReplicaTransition("T", 0).
			Plan("t/v1").Group("G").DisplayName("T").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx(&testReplicaCtx{id: 0, name: "rv-1-0"}))
		e.Finalize()

		Expect(func() { e.CreateReplicaTransition("T", "t/v1", 0) }).To(Panic())
	})
})

var _ = Describe("CreateGlobalTransition", func() {
	It("creates a global transition", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		reg.GlobalTransition("EnableMultiattach").
			Plan("enable/v1").Group("Multiattach").DisplayName("Enabling").
			Steps(GlobalStep("Enable", stubGlobalApply, stubGlobalConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())

		t, reason := e.CreateGlobalTransition("EnableMultiattach", "enable/v1")

		Expect(reason).To(BeEmpty())
		Expect(t).NotTo(BeNil())
		Expect(t.Type).To(Equal(TransitionType("EnableMultiattach")))
		Expect(rev).To(Equal(int64(1)))
	})

	It("blocks when guard fails", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		reg.GlobalTransition("ChangeQuorum").
			Plan("qmr/v1").Group("Quorum").DisplayName("Changing quorum").
			Guards(func(*testGCtx) GuardResult {
				return GuardResult{Blocked: true, Message: "not safe"}
			}).
			Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())

		t, reason := e.CreateGlobalTransition("ChangeQuorum", "qmr/v1")

		Expect(t).To(BeNil())
		Expect(reason).To(Equal("not safe"))
	})

	It("blocks when tracker denies admission", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()

		reg.GlobalTransition("ChangeQuorum").
			Plan("qmr/v1").Group("Quorum").DisplayName("Changing quorum").
			Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).Build()

		tracker := &mockTracker{canAdmitFn: func(_ *Transition) (bool, string, any) {
			return false, "quorum change in progress", nil
		}}

		var rev int64
		e := newTestEngine(reg, tracker, &rev, nil, testCtx())

		t, reason := e.CreateGlobalTransition("ChangeQuorum", "qmr/v1")

		Expect(t).To(BeNil())
		Expect(reason).To(Equal("quorum change in progress"))
	})

	It("panics on unknown plan", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())

		Expect(func() { e.CreateGlobalTransition("Unknown", "x/v1") }).To(Panic())
	})

	It("panics when already finalized", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.GlobalTransition("T").
			Plan("t/v1").Group("G").DisplayName("T").
			Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())
		e.Finalize()

		Expect(func() { e.CreateGlobalTransition("T", "t/v1") }).To(Panic())
	})
})

var _ = Describe("Finalize", func() {
	It("returns original slice when nothing changed", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		transitions := []Transition{newTestTransition("Unknown", "x/v1", "rv-1-0")}
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx())

		e.Process(context.Background())
		result := e.Finalize()

		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("Unknown")))
	})

	It("compacts after deletion", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Auto-confirming plan → transition completes during Process.
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("T").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		var rev int64
		transitions := []Transition{
			newTestTransition("AddReplica", "access/v1", "rv-1-0"),
		}
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx(rctx0))

		e.Process(context.Background())
		result := e.Finalize()

		Expect(result).To(BeEmpty())
	})

	It("includes newly created transitions", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.GlobalTransition("EnableMultiattach").
			Plan("enable/v1").Group("Multiattach").DisplayName("Enabling").
			Steps(GlobalStep("Enable", stubGlobalApply, stubGlobalConfirm)).Build()

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())

		e.CreateGlobalTransition("EnableMultiattach", "enable/v1")
		result := e.Finalize()

		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("EnableMultiattach")))
	})

	It("updates slot pointers to point into returned slice", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, neverReplicaConfirm)).Build()

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx(rctx5))

		e.CreateReplicaTransition("AddReplica", "access/v1", 5)
		result := e.Finalize()

		// Slot pointer should point into the returned slice, not the heap.
		Expect(slot.activeTransitions[5]).To(BeIdenticalTo(&result[0]))
	})

	It("panics on double Finalize", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())
		e.Finalize()

		Expect(func() { e.Finalize() }).To(Panic())
	})

	It("panics on Process after Finalize", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, nil, testCtx())
		e.Finalize()

		Expect(func() { e.Process(context.Background()) }).To(Panic())
	})

	It("updates slot pointers after deletion shifts surviving transitions", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Auto-confirming plan → completes during settle.
		reg.ReplicaTransition("AutoDone", 0).
			Plan("auto/v1").Group("G").DisplayName("Auto").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		// Never-confirming plan → survives settle.
		reg.ReplicaTransition("Persist", 0).
			Plan("persist/v1").Group("G").DisplayName("Persist").
			Steps(ReplicaStep("s", stubReplicaApply, neverReplicaConfirm)).Build()

		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}

		transitions := []Transition{
			newTestTransition("AutoDone", "auto/v1", "rv-1-0"),
			newTestTransition("Persist", "persist/v1", "rv-1-3"),
		}

		var rev int64
		e := newTestEngine(reg, &mockTracker{}, &rev, transitions, testCtx(rctx0, rctx3))

		e.Process(context.Background())
		result := e.Finalize()

		// First transition completed → deleted, second shifted to index 0.
		Expect(result).To(HaveLen(1))
		Expect(result[0].Type).To(Equal(TransitionType("Persist")))
		// Slot pointer must reference the returned slice element, not the old address.
		Expect(slot.activeTransitions[3]).To(BeIdenticalTo(&result[0]))
	})

	It("updates slot pointers when deletion and creation exceed original capacity", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Auto-confirming plan → completes during settle.
		reg.ReplicaTransition("AutoDone", 0).
			Plan("auto/v1").Group("G").DisplayName("Auto").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		// Never-confirming plan → survives settle / stays after dispatch.
		reg.ReplicaTransition("Persist", 0).
			Plan("persist/v1").Group("G").DisplayName("Persist").
			Steps(ReplicaStep("s", stubReplicaApply, neverReplicaConfirm)).Build()

		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		rctx7 := &testReplicaCtx{id: 7, name: "rv-1-7"}

		// Two initial transitions with cap=2: first auto-confirms (deleted),
		// second persists. Dispatch adds two more → total 3 > original 2,
		// forcing slices.Grow to reallocate.
		transitions := make([]Transition, 2)
		transitions[0] = newTestTransition("AutoDone", "auto/v1", "rv-1-0")
		transitions[1] = newTestTransition("Persist", "persist/v1", "rv-1-3")

		dispatcher := func(_ testCtxProvider) iter.Seq[DispatchDecision] {
			return func(yield func(DispatchDecision) bool) {
				if !yield(DispatchReplica(rctx5, "Persist", "persist/v1")) {
					return
				}
				yield(DispatchReplica(rctx7, "Persist", "persist/v1"))
			}
		}

		var rev int64
		e := NewEngine(context.Background(), reg, &mockTracker{},
			[]DispatchFunc[testCtxProvider]{dispatcher}, &rev, transitions,
			testCtx(rctx0, rctx3, rctx5, rctx7))

		e.Process(context.Background())
		result := e.Finalize()

		// 1 deleted + 1 surviving + 2 new = 3 transitions.
		Expect(result).To(HaveLen(3))
		// All slot pointers must reference the returned slice, not stale memory.
		Expect(slot.activeTransitions[3]).To(BeIdenticalTo(&result[0]))
		Expect(slot.activeTransitions[5]).To(BeIdenticalTo(&result[1]))
		Expect(slot.activeTransitions[7]).To(BeIdenticalTo(&result[2]))
	})

	It("handles deletion and creation in the same Process", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		// Auto-confirming plan — existing transition will complete.
		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("Joining").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		rctx0 := &testReplicaCtx{id: 0, name: "rv-1-0"}
		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}

		var rev int64
		transitions := []Transition{newTestTransition("AddReplica", "access/v1", "rv-1-0")}
		e := NewEngine(context.Background(), reg, &mockTracker{}, []DispatchFunc[testCtxProvider]{singleReplicaDispatcher(rctx5, "AddReplica", "access/v1")}, &rev, transitions, testCtx(rctx0, rctx5))

		changed := e.Process(context.Background())

		Expect(changed).To(BeTrue())
		result := e.Finalize()
		// Existing transition completed (deleted), new transition created.
		Expect(result).To(HaveLen(1))
		Expect(result[0].ReplicaName).To(Equal("rv-1-5"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Engine internal helpers
//

var _ = Describe("createTransition", func() {
	It("builds a replica transition with all metadata", func() {
		var rev int64
		p := newTestReplicaPlan()
		e := newTestEngineWithPlan(p, "diskful-odd/v1", &rev, nil, testCtx())
		rctx := &testReplicaCtx{id: 7, name: "rv-1-7"}

		t := e.createTransition(p, "diskful-odd/v1", rctx)

		Expect(t).NotTo(BeNil())
		Expect(t.Type).To(Equal(TransitionType("AddReplica")))
		Expect(t.Group).To(Equal(TransitionGroup("VotingMembership")))
		Expect(t.PlanID).To(Equal("diskful-odd/v1"))
		Expect(t.ReplicaType).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(t.ReplicaName).To(Equal("rv-1-7"))
	})

	It("builds steps with correct statuses", func() {
		var rev int64
		p := newTestReplicaPlan()
		e := newTestEngineWithPlan(p, "diskful-odd/v1", &rev, nil, testCtx())

		t := e.createTransition(p, "diskful-odd/v1", &testReplicaCtx{id: 0, name: "rv-1-0"})

		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[0].StartedAt).NotTo(BeNil())
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("builds a global transition", func() {
		var rev int64
		p := newTestGlobalPlan()
		e := newTestEngineWithPlan(p, "enable/v1", &rev, nil, testCtx())

		t := e.createTransition(p, "enable/v1", (*testReplicaCtx)(nil))

		Expect(t.ReplicaName).To(BeEmpty())
		Expect(t.Steps).To(HaveLen(1))
		Expect(t.ReplicaType).To(BeEmpty())
		Expect(t.FromReplicaType).To(BeEmpty())
		Expect(t.ToReplicaType).To(BeEmpty())
	})

	It("panics on empty rctx.Name() for replica plan", func() {
		var rev int64
		p := newTestReplicaPlan()
		e := newTestEngineWithPlan(p, "test/v1", &rev, nil, testCtx())

		Expect(func() { e.createTransition(p, "test/v1", &testReplicaCtx{id: 2, name: ""}) }).To(Panic())
	})
})

var _ = Describe("applyStep", func() {
	It("applies step, bumps revision, sets DatameshRevision", func() {
		applyCalled := false
		p := &plan[*testGCtx, *testReplicaCtx]{
			transitionType: "T",
			steps: []step[*testGCtx, *testReplicaCtx]{
				{name: "s", scope: ReplicaScope, replicaApply: func(*testGCtx, *testReplicaCtx) { applyCalled = true }, replicaConfirm: stubReplicaConfirm},
			},
		}
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Name: "s"}}}

		var rev int64
		e := newTestEngineWithPlan(p, "test/v1", &rev, nil, testCtx())

		e.applyStep(p, t, &testReplicaCtx{}, 0)

		Expect(applyCalled).To(BeTrue())
		Expect(rev).To(Equal(int64(1)))
		Expect(t.Steps[0].DatameshRevision).To(Equal(int64(1)))
	})
})

var _ = Describe("confirmStep", func() {
	It("returns ConfirmResult and composed message", func() {
		p := &plan[*testGCtx, *testReplicaCtx]{
			displayName:    "Test",
			transitionType: "T",
			steps: []step[*testGCtx, *testReplicaCtx]{
				{name: "s", scope: ReplicaScope, replicaApply: stubReplicaApply, replicaConfirm: stubReplicaConfirm},
			},
		}
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Name: "s", DatameshRevision: 1}}}

		var rev int64 = 1
		e := newTestEngineWithPlan(p, "test/v1", &rev, nil, testCtx())

		cr, composed := e.confirmStep(p, t, &testReplicaCtx{}, 0, nil)

		Expect(cr.MustConfirm.Len()).To(Equal(0))
		Expect(composed).To(Equal("Test: 0/0 replicas confirmed revision 1"))
		Expect(t.Steps[0].Message).To(Equal("0/0 replicas confirmed revision 1"))
	})

	It("sets changed when message differs", func() {
		p := &plan[*testGCtx, *testReplicaCtx]{
			displayName:    "Test",
			transitionType: "T",
			steps: []step[*testGCtx, *testReplicaCtx]{
				{name: "s", scope: ReplicaScope, replicaApply: stubReplicaApply, replicaConfirm: stubReplicaConfirm},
			},
		}
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Name: "s", DatameshRevision: 1, Message: "old message"}}}

		var rev int64 = 1
		e := newTestEngineWithPlan(p, "test/v1", &rev, nil, testCtx())

		changed := false
		e.confirmStep(p, t, &testReplicaCtx{}, 0, &changed)

		Expect(changed).To(BeTrue())
	})

	It("does not set changed when message is the same", func() {
		p := &plan[*testGCtx, *testReplicaCtx]{
			displayName:    "Test",
			transitionType: "T",
			steps: []step[*testGCtx, *testReplicaCtx]{
				{name: "s", scope: ReplicaScope, replicaApply: stubReplicaApply, replicaConfirm: stubReplicaConfirm},
			},
		}
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Name: "s", DatameshRevision: 1, Message: "0/0 replicas confirmed revision 1"}}}

		var rev int64 = 1
		e := newTestEngineWithPlan(p, "test/v1", &rev, nil, testCtx())

		changed := false
		e.confirmStep(p, t, &testReplicaCtx{}, 0, &changed)

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("advanceStep", func() {
	It("completes step 0 and activates step 1", func() {
		now := metav1.Now()
		t := &Transition{
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "s0", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &now, Message: "in progress"},
				{Name: "s1", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}

		advanceStep(t, 0)

		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(t.Steps[0].CompletedAt).NotTo(BeNil())
		Expect(t.Steps[0].Message).To(Equal("in progress"))
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[1].StartedAt).NotTo(BeNil())
	})
})

var _ = Describe("findCurrentStepIdx", func() {
	It("returns first non-Completed step", func() {
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
		}}
		Expect(findCurrentStepIdx(t)).To(Equal(1))
	})

	It("returns -1 when all steps completed", func() {
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
		}}
		Expect(findCurrentStepIdx(t)).To(Equal(-1))
	})

	It("returns 0 for single active step", func() {
		t := &Transition{Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
		}}
		Expect(findCurrentStepIdx(t)).To(Equal(0))
	})
})

var _ = Describe("deleteTransition", func() {
	It("removes transition from shadow index and unregisters from tracker", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		slot := newRecordingSlotAccessor()
		reg.RegisterReplicaSlot(0, slot)

		reg.ReplicaTransition("AddReplica", 0).
			Plan("access/v1").Group("G").DisplayName("T").
			Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

		tracker := &mockTracker{}
		var rev int64
		rctx3 := &testReplicaCtx{id: 3, name: "rv-1-3"}
		transitions := []Transition{
			newTestTransition("AddReplica", "access/v1", "rv-1-3"),
			newTestTransition("AddReplica", "access/v1", "rv-1-5"),
		}

		rctx5 := &testReplicaCtx{id: 5, name: "rv-1-5"}
		e := newTestEngine(reg, tracker, &rev, transitions, testCtx(rctx3, rctx5))

		// Clear slot for rv-1-3 (caller responsibility), then delete.
		slot.SetActiveTransition(rctx3, nil)
		e.deleteTransition(0)

		Expect(e.transitions).To(HaveLen(1))
		Expect(e.transitions[0].ReplicaName).To(Equal("rv-1-5"))
		// Slot pointer for rv-1-3 cleared by caller; rv-1-5 still valid.
		Expect(slot.activeTransitions).NotTo(HaveKey(uint8(3)))
		Expect(slot.activeTransitions).To(HaveKey(uint8(5)))
		// Tracker.Remove should have been called.
		Expect(tracker.removed).To(HaveLen(1))
	})
})
