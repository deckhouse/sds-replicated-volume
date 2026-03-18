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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Registry", func() {
	It("NewRegistry creates empty registry", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(reg).NotTo(BeNil())
		Expect(reg.plans).To(BeEmpty())
	})

	It("RegisterReplicaSlot stores accessor", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(0, testSlotAccessor{})
		Expect(reg.replicaSlotAccessors[0]).NotTo(BeNil())
	})

	It("RegisterReplicaSlot panics on duplicate", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(0, testSlotAccessor{})
		Expect(func() { reg.RegisterReplicaSlot(0, testSlotAccessor{}) }).To(Panic())
	})

	It("RegisterReplicaSlot panics on out-of-range", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(func() { reg.RegisterReplicaSlot(MaxReplicaSlots, testSlotAccessor{}) }).To(Panic())
	})

	It("RegisterReplicaSlot panics on nil accessor", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(func() { reg.RegisterReplicaSlot(0, nil) }).To(Panic())
	})

	It("unregistered slot has nil accessor", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(reg.replicaSlotAccessors[0]).To(BeNil())
	})

	It("replicaSlotAccessor returns accessor for registered slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		reg.RegisterReplicaSlot(1, testSlotAccessor{})
		Expect(reg.replicaSlotAccessor(1)).NotTo(BeNil())
	})

	It("replicaSlotAccessor returns nil for unregistered slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(reg.replicaSlotAccessor(0)).To(BeNil())
	})

	It("replicaSlotAccessor returns nil for out-of-range slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(reg.replicaSlotAccessor(MaxReplicaSlots)).To(BeNil())
		Expect(reg.replicaSlotAccessor(255)).To(BeNil())
	})

	It("ReplicaTransition returns handle with replica scope and slot", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		rt := reg.ReplicaTransition("AddReplica", 0)
		Expect(rt.scope).To(Equal(ReplicaScope))
		Expect(rt.transitionType).To(Equal(TransitionType("AddReplica")))
		Expect(rt.slot).To(Equal(ReplicaSlotID(0)))
	})

	It("GlobalTransition returns handle with global scope", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		rt := reg.GlobalTransition("EnableMultiattach")
		Expect(rt.scope).To(Equal(GlobalScope))
		Expect(rt.transitionType).To(Equal(TransitionType("EnableMultiattach")))
	})

	It("get returns plan after Build", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		rt := reg.ReplicaTransition("AddReplica", 0)
		rt.Plan("access/v1").
			Group("NonVotingMembership").
			DisplayName("Test").
			Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).
			Build()

		p := reg.get("AddReplica", "access/v1")
		Expect(p).NotTo(BeNil())
		Expect(p.displayName).To(Equal("Test"))
	})

	It("get returns nil for unknown plan", func() {
		reg := NewRegistry[*testGCtx, *testReplicaCtx]()
		Expect(reg.get("Unknown", "x/v1")).To(BeNil())
	})

	Describe("PlanStepCount", func() {
		It("returns step count for registered plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("AddReplica", 0)
			rt.Plan("diskful/v1").
				Group("VotingMembership").
				DisplayName("Adding diskful").
				Steps(
					ReplicaStep("✦ → D∅", stubReplicaApply, stubReplicaConfirm),
					ReplicaStep("D∅ → D", stubReplicaApply, stubReplicaConfirm),
				).
				Build()

			Expect(reg.PlanStepCount("AddReplica", "diskful/v1")).To(Equal(2))
		})

		It("returns 0 for unknown plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(reg.PlanStepCount("Unknown", "x/v1")).To(Equal(0))
		})

		It("returns 0 for unknown transition type with existing plan ID", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("AddReplica", 0)
			rt.Plan("access/v1").
				Group("NonVotingMembership").
				DisplayName("Test").
				Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).
				Build()

			Expect(reg.PlanStepCount("RemoveReplica", "access/v1")).To(Equal(0))
		})
	})

	Describe("MaxPlanStepCount", func() {
		It("returns max step count across plans for a transition type", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("AddReplica", 0)

			rt.Plan("access/v1").
				Group("NonVotingMembership").
				DisplayName("Add A").
				Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).
				Build()

			rt.Plan("diskful/v1").
				Group("VotingMembership").
				DisplayName("Add D").
				Steps(
					ReplicaStep("✦ → D∅", stubReplicaApply, stubReplicaConfirm),
					ReplicaStep("D∅ → D", stubReplicaApply, stubReplicaConfirm),
				).
				Build()

			rt.Plan("diskful-q-up/v1").
				Group("VotingMembership").
				DisplayName("Add D+q").
				Steps(
					ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm),
					ReplicaStep("A → D∅", stubReplicaApply, stubReplicaConfirm),
					ReplicaStep("D∅ → D", stubReplicaApply, stubReplicaConfirm),
				).
				Build()

			Expect(reg.MaxPlanStepCount("AddReplica")).To(Equal(3))
		})

		It("returns 0 for unknown transition type", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(reg.MaxPlanStepCount("Unknown")).To(Equal(0))
		})

		It("ignores plans of other transition types", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()

			addRT := reg.ReplicaTransition("AddReplica", 0)
			addRT.Plan("access/v1").
				Group("NonVotingMembership").
				DisplayName("Add A").
				Steps(ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)).
				Build()

			removeRT := reg.ReplicaTransition("RemoveReplica", 0)
			removeRT.Plan("access/v1").
				Group("NonVotingMembership").
				DisplayName("Remove A").
				Steps(ReplicaStep("A → ✕", stubReplicaApply, stubReplicaConfirm)).
				Build()

			Expect(reg.MaxPlanStepCount("AddReplica")).To(Equal(1))
			Expect(reg.MaxPlanStepCount("RemoveReplica")).To(Equal(1))
		})
	})
})
