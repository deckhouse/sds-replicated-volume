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

package dme

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Shared stub callbacks for tests that don't care about apply/confirm logic.
var (
	stubGlobalApply    GlobalApplyFunc    = func(*GlobalContext) {}
	stubGlobalConfirm  GlobalConfirmFunc  = func(*GlobalContext, int64) ConfirmResult { return ConfirmResult{} }
	stubReplicaApply   ReplicaApplyFunc   = func(*GlobalContext, *ReplicaContext) {}
	stubReplicaConfirm ReplicaConfirmFunc = func(*GlobalContext, *ReplicaContext, int64) ConfirmResult {
		return ConfirmResult{} // empty MustConfirm == empty Confirmed → auto-confirmed
	}
)

// stubMembershipPlanner is a minimal MembershipPlanner implementation for tests.
type stubMembershipPlanner struct{}

func (stubMembershipPlanner) Classify(v1alpha1.DatameshMembershipRequestOperation) v1alpha1.ReplicatedVolumeDatameshTransitionType {
	return ""
}

func (stubMembershipPlanner) Plan(*GlobalContext, *ReplicaContext, v1alpha1.ReplicatedVolumeDatameshTransitionType) (PlanID, string) {
	return "", "not implemented"
}

// stubAttachmentPlanner is a minimal AttachmentPlanner implementation for tests.
type stubAttachmentPlanner struct{}

func (stubAttachmentPlanner) PlanAttachments(*GlobalContext, []*ReplicaContext) []AttachmentDecision {
	return nil
}

func (stubAttachmentPlanner) PlanMultiattach(*GlobalContext) *MultiattachDecision {
	return nil
}

var _ = Describe("NewEngine", func() {
	It("panics with nil registry", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(func() { NewEngine(nil, nil, nil, rv, nil, nil, nil, FeatureFlags{}) }).To(Panic())
	})

	It("builds replica contexts from RV state", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-a"},
						{Name: "rv-1-3", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-b"},
					},
				},
			},
		}

		reg := NewRegistry()
		eng := NewEngine(reg, stubMembershipPlanner{}, stubAttachmentPlanner{}, rv, nil, nil, nil, FeatureFlags{})

		rc0 := eng.replicaCtxByID[0]
		Expect(rc0).NotTo(BeNil())
		Expect(rc0.NodeName).To(Equal("node-a"))
		Expect(rc0.Member).NotTo(BeNil())
		Expect(rc0.Member.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))

		rc3 := eng.replicaCtxByID[3]
		Expect(rc3).NotTo(BeNil())
		Expect(rc3.NodeName).To(Equal("node-b"))
		Expect(rc3.Member).NotTo(BeNil())
		Expect(rc3.Member.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))

		Expect(eng.replicaCtxByID[5]).To(BeNil())
	})
})
