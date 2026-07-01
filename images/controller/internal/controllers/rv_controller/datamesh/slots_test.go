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

package datamesh

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

var _ = Describe("Slot constants", func() {
	It("membershipSlot is 0", func() {
		Expect(membershipSlot).To(Equal(dmte.ReplicaSlotID(0)))
	})

	It("attachmentSlot is 1", func() {
		Expect(attachmentSlot).To(Equal(dmte.ReplicaSlotID(1)))
	})
})

var _ = Describe("membershipSlotAccessor", func() {
	var (
		slot membershipSlotAccessor
		rctx *ReplicaContext
	)

	BeforeEach(func() {
		slot = membershipSlotAccessor{}
		rctx = &ReplicaContext{}
	})

	It("GetActiveTransition returns nil when no transition", func() {
		Expect(slot.GetActiveTransition(rctx)).To(BeNil())
	})

	It("SetActiveTransition stores the transition", func() {
		t := &dmte.Transition{Type: "AddReplica"}
		slot.SetActiveTransition(rctx, t)
		Expect(rctx.membershipTransition).To(Equal(t))
		Expect(slot.GetActiveTransition(rctx)).To(Equal(t))
	})

	It("SetActiveTransition clears with nil", func() {
		rctx.membershipTransition = &dmte.Transition{Type: "AddReplica"}
		slot.SetActiveTransition(rctx, nil)
		Expect(rctx.membershipTransition).To(BeNil())
	})

	It("SetStatus writes membershipMessage", func() {
		slot.SetStatus(rctx, "Joining datamesh", nil)
		Expect(rctx.membershipMessage).To(Equal("Joining datamesh"))
	})

	It("SetStatus ignores details", func() {
		slot.SetStatus(rctx, "msg", "some-detail")
		Expect(rctx.membershipMessage).To(Equal("msg"))
	})
})

var _ = Describe("attachmentSlotAccessor", func() {
	var (
		slot attachmentSlotAccessor
		rctx *ReplicaContext
	)

	BeforeEach(func() {
		slot = attachmentSlotAccessor{}
		rctx = &ReplicaContext{}
	})

	It("GetActiveTransition returns nil when no transition", func() {
		Expect(slot.GetActiveTransition(rctx)).To(BeNil())
	})

	It("SetActiveTransition stores the transition", func() {
		t := &dmte.Transition{Type: "Attach"}
		slot.SetActiveTransition(rctx, t)
		Expect(rctx.attachmentTransition).To(Equal(t))
		Expect(slot.GetActiveTransition(rctx)).To(Equal(t))
	})

	It("SetActiveTransition clears with nil", func() {
		rctx.attachmentTransition = &dmte.Transition{Type: "Attach"}
		slot.SetActiveTransition(rctx, nil)
		Expect(rctx.attachmentTransition).To(BeNil())
	})

	It("SetStatus writes attachmentConditionMessage and reason from string details", func() {
		slot.SetStatus(rctx, "Attaching volume", "Attaching")
		Expect(rctx.AttachmentConditionMessage()).To(Equal("Attaching volume"))
		Expect(rctx.AttachmentConditionReason()).To(Equal("Attaching"))
	})

	It("SetStatus with non-string details does not change reason", func() {
		rctx.attachmentConditionReason = "Previous"
		slot.SetStatus(rctx, "msg", 42)
		Expect(rctx.AttachmentConditionMessage()).To(Equal("msg"))
		Expect(rctx.AttachmentConditionReason()).To(Equal("Previous"))
	})

	It("SetStatus with nil details does not change reason", func() {
		rctx.attachmentConditionReason = "Previous"
		slot.SetStatus(rctx, "msg", nil)
		Expect(rctx.AttachmentConditionMessage()).To(Equal("msg"))
		Expect(rctx.AttachmentConditionReason()).To(Equal("Previous"))
	})
})
