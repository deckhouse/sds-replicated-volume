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

var _ = Describe("PlanBuilder", func() {
	Describe("full DSL", func() {
		It("registers a replica plan with all options", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("AddReplica", 0)

			completeCalled := false
			rt.Plan("access/v1").
				Group("NonVotingMembership").
				DisplayName("Joining datamesh").
				Guards(
					func(*testGCtx, *testReplicaCtx) GuardResult {
						return GuardResult{}
					},
				).
				Steps(
					ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).
						Details("Attaching"),
				).
				OnComplete(func(*testGCtx, *testReplicaCtx) { completeCalled = true }).
				Build()

			p := reg.get("AddReplica", "access/v1")
			Expect(p).NotTo(BeNil())
			Expect(p.scope).To(Equal(ReplicaScope))
			Expect(p.group).To(Equal(TransitionGroup("NonVotingMembership")))
			Expect(p.displayName).To(Equal("Joining datamesh"))
			Expect(p.steps).To(HaveLen(1))
			Expect(p.steps[0].name).To(Equal("✦ → A"))
			Expect(p.steps[0].details).To(Equal("Attaching"))
			Expect(p.replicaGuards).To(HaveLen(1))

			p.callOnComplete(&testGCtx{}, &testReplicaCtx{})
			Expect(completeCalled).To(BeTrue())
		})

		It("registers a global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.GlobalTransition("EnableMultiattach")

			completeCalled := false
			rt.Plan("enable/v1").
				Group("Multiattach").
				DisplayName("Enabling multiattach").
				Steps(
					GlobalStep("Enable", stubGlobalApply, stubGlobalConfirm),
				).
				OnComplete(func(*testGCtx) { completeCalled = true }).
				Build()

			p := reg.get("EnableMultiattach", "enable/v1")
			Expect(p).NotTo(BeNil())
			Expect(p.scope).To(Equal(GlobalScope))
			Expect(p.group).To(Equal(TransitionGroup("Multiattach")))
			Expect(p.displayName).To(Equal("Enabling multiattach"))
			Expect(p.steps).To(HaveLen(1))

			p.callOnComplete(&testGCtx{}, &testReplicaCtx{})
			Expect(completeCalled).To(BeTrue())
		})

		It("registers multi-step mixed plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("AddReplica", 0)
			rt.Plan("diskful-odd/v1").
				Group("VotingMembership").
				DisplayName("Adding diskful").
				Steps(
					ReplicaStep("✦ → D∅", stubReplicaApply, stubReplicaConfirm),
					GlobalStep("qmr↑", stubGlobalApply, stubGlobalConfirm),
					ReplicaStep("D∅ → D", stubReplicaApply, stubReplicaConfirm),
				).
				Build()

			p := reg.get("AddReplica", "diskful-odd/v1")
			Expect(p).NotTo(BeNil())
			Expect(p.steps).To(HaveLen(3))
			Expect(p.steps[0].scope).To(Equal(ReplicaScope))
			Expect(p.steps[1].scope).To(Equal(GlobalScope))
			Expect(p.steps[2].scope).To(Equal(ReplicaScope))
		})
	})

	Describe("PlanID validation", func() {
		It("valid PlanIDs", func() {
			for _, id := range []PlanID{"access/v1", "diskful-odd/v1", "a/v1", "my-plan/v23", "a-b-c/v100"} {
				reg := NewRegistry[*testGCtx, *testReplicaCtx]()
				Expect(func() {
					replicaPlan(reg, id).
						Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).
						Build()
				}).NotTo(Panic(), "PlanID %q should be valid", id)
			}
		})

		It("invalid PlanIDs", func() {
			for _, id := range []PlanID{"access", "access/", "/v1", "access/v0", "", "Access/v1", "access/V1", "access/v", "access/v01"} {
				reg := NewRegistry[*testGCtx, *testReplicaCtx]()
				Expect(func() {
					replicaPlan(reg, id).
						Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).
						Build()
				}).To(Panic(), "PlanID %q should be invalid", id)
			}
		})
	})

	Describe("Build panics", func() {
		It("panics on no steps", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				replicaPlan(reg, "test/v1").Build()
			}).To(Panic())
		})

		It("panics on duplicate key", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("dup/v1").Group("G").
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			Expect(func() {
				rt.Plan("dup/v1").Group("G").
					Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			}).To(Panic())
		})

		It("panics on Group not set", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			Expect(func() {
				rt.Plan("test/v1"). // no Group
							Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			}).To(Panic())
		})

		It("panics on ReplicaStep in global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				globalPlan(reg, "test/v1").
					Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			}).To(Panic())
		})

		It("panics on wrong guard type for replica plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				replicaPlan(reg, "test/v1").
					Guards(func(*testGCtx) GuardResult { return GuardResult{} }).
					Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			}).To(Panic())
		})

		It("panics on wrong guard type for global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				globalPlan(reg, "test/v1").
					Guards(func(*testGCtx, *testReplicaCtx) GuardResult { return GuardResult{} }).
					Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).Build()
			}).To(Panic())
		})

		It("panics on wrong OnComplete type for replica plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				replicaPlan(reg, "test/v1").
					Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).
					OnComplete(func(*testGCtx) {}). // should be func(G, R)
					Build()
			}).To(Panic())
		})

		It("panics on wrong OnComplete type for global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				globalPlan(reg, "test/v1").
					Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).
					OnComplete(func(*testGCtx, *testReplicaCtx) {}). // should be func(G)
					Build()
			}).To(Panic())
		})

		It("panics on wrong Init type for replica plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				replicaPlan(reg, "test/v1").
					Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).
					Init(func(*testGCtx, *Transition) {}). // should be func(G, R, *Transition)
					Build()
			}).To(Panic())
		})

		It("panics on wrong Init type for global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				globalPlan(reg, "test/v1").
					Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).
					Init(func(*testGCtx, *testReplicaCtx, *Transition) {}). // should be func(G, *Transition)
					Build()
			}).To(Panic())
		})

		It("panics on CancelActiveOnCreate for global plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				globalPlan(reg, "test/v1").
					Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).
					CancelActiveOnCreate(true).Build()
			}).To(Panic())
		})

		It("panics on unsupported step type", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			Expect(func() {
				replicaPlan(reg, "test/v1").
					Steps("not a step builder").Build()
			}).To(Panic())
		})

	})

	Describe("metadata", func() {
		It("CancelActiveOnCreate stored for replica plan", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("test/v1").Group("G").
				CancelActiveOnCreate(true).
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()
			p := reg.get("T", "test/v1")
			Expect(p.cancelActiveOnCreate).To(BeTrue())
		})
	})

	Describe("guards evaluation", func() {
		It("evaluates replica guards in order, first blocking wins", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("test/v1").Group("G").
				Guards(
					func(*testGCtx, *testReplicaCtx) GuardResult {
						return GuardResult{} // pass
					},
					func(*testGCtx, *testReplicaCtx) GuardResult {
						return GuardResult{Blocked: true, Message: "blocked by guard 2"}
					},
					func(*testGCtx, *testReplicaCtx) GuardResult {
						return GuardResult{Blocked: true, Message: "should not reach"}
					},
				).
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("T", "test/v1")
			result := p.evaluateGuards(&testGCtx{}, &testReplicaCtx{})
			Expect(result.Blocked).To(BeTrue())
			Expect(result.Message).To(Equal("blocked by guard 2"))
		})

		It("evaluates global guards in order, first blocking wins", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.GlobalTransition("T")
			rt.Plan("test/v1").Group("G").
				Guards(
					func(*testGCtx) GuardResult {
						return GuardResult{} // pass
					},
					func(*testGCtx) GuardResult {
						return GuardResult{Blocked: true, Message: "global guard blocked"}
					},
				).
				Steps(GlobalStep("s", stubGlobalApply, stubGlobalConfirm)).Build()

			p := reg.get("T", "test/v1")
			result := p.evaluateGuards(&testGCtx{}, &testReplicaCtx{})
			Expect(result.Blocked).To(BeTrue())
			Expect(result.Message).To(Equal("global guard blocked"))
		})

		It("all guards pass", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("test/v1").Group("G").
				Guards(
					func(*testGCtx, *testReplicaCtx) GuardResult {
						return GuardResult{}
					},
				).
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("T", "test/v1")
			result := p.evaluateGuards(&testGCtx{}, &testReplicaCtx{})
			Expect(result.Blocked).To(BeFalse())
		})

		It("no guards always passes", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("test/v1").Group("G").
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("T", "test/v1")
			result := p.evaluateGuards(&testGCtx{}, &testReplicaCtx{})
			Expect(result.Blocked).To(BeFalse())
		})

		It("panics on invalid scope", func() {
			p := &plan[*testGCtx, *testReplicaCtx]{scope: Scope(99)}
			Expect(func() { p.evaluateGuards(&testGCtx{}, &testReplicaCtx{}) }).To(Panic())
		})
	})

	Describe("callOnComplete", func() {
		It("no-op when no OnComplete set", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 0)
			rt.Plan("test/v1").Group("G").
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("T", "test/v1")
			Expect(func() { p.callOnComplete(&testGCtx{}, &testReplicaCtx{}) }).NotTo(Panic())
		})

		It("panics on invalid scope", func() {
			p := &plan[*testGCtx, *testReplicaCtx]{scope: Scope(99)}
			Expect(func() { p.callOnComplete(&testGCtx{}, &testReplicaCtx{}) }).To(Panic())
		})
	})

	Describe("inherited fields", func() {
		It("plan inherits slot from ReplicaTransition", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("T", 1)
			rt.Plan("test/v1").Group("G").
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("T", "test/v1")
			Expect(p.slot).To(Equal(ReplicaSlotID(1)))
		})

		It("plan inherits transitionType from RegisteredTransition", func() {
			reg := NewRegistry[*testGCtx, *testReplicaCtx]()
			rt := reg.ReplicaTransition("MyType", 0)
			rt.Plan("test/v1").Group("G").
				Steps(ReplicaStep("s", stubReplicaApply, stubReplicaConfirm)).Build()

			p := reg.get("MyType", "test/v1")
			Expect(p.transitionType).To(Equal(TransitionType("MyType")))
		})
	})
})
