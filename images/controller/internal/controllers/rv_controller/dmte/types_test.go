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

var _ = Describe("DispatchDecision constructors", func() {
	It("DispatchReplica sets replicaCtx, transitionType, planID", func() {
		rctx := &testReplicaCtx{id: 3}
		d := DispatchReplica(rctx, "AddReplica", "access/v1")
		Expect(d.replicaCtx).To(BeIdenticalTo(rctx))
		Expect(d.transitionType).To(Equal(TransitionType("AddReplica")))
		Expect(d.planID).To(Equal(PlanID("access/v1")))
		Expect(d.message).To(BeEmpty())
		Expect(d.details).To(BeNil())
	})

	It("DispatchGlobal sets transitionType, planID, nil replicaCtx", func() {
		d := DispatchGlobal("EnableMultiattach", "enable/v1")
		Expect(d.replicaCtx).To(BeNil())
		Expect(d.transitionType).To(Equal(TransitionType("EnableMultiattach")))
		Expect(d.planID).To(Equal(PlanID("enable/v1")))
	})

	It("NoDispatch sets replicaCtx, slot, message, details, empty planID", func() {
		rctx := &testReplicaCtx{id: 5}
		d := NoDispatch(rctx, 1, "Volume is attached", "Attached")
		Expect(d.replicaCtx).To(BeIdenticalTo(rctx))
		Expect(d.replicaSlot).To(Equal(ReplicaSlotID(1)))
		Expect(d.message).To(Equal("Volume is attached"))
		Expect(d.details).To(Equal("Attached"))
		Expect(d.planID).To(Equal(PlanID("")))
		Expect(d.transitionType).To(Equal(TransitionType("")))
	})
})
