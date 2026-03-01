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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("ComposeBlockedByActiveMessage", func() {
	It("composes message with active step progress", func() {
		req := &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name:    "rv-1-5",
			Request: v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationLeave},
		}
		activeTx := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now()), Message: "3/4 confirmed"},
			},
		}

		msg := ComposeBlockedByActiveMessage(req, activeTx)
		Expect(msg).To(ContainSubstring("Leave"))
		Expect(msg).To(ContainSubstring("AddReplica"))
		Expect(msg).To(ContainSubstring("3/4 confirmed"))
	})

	It("composes message without step progress", func() {
		req := &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name:    "rv-1-5",
			Request: v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationChangeRole},
		}
		activeTx := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → ✕", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
			},
		}

		msg := ComposeBlockedByActiveMessage(req, activeTx)
		Expect(msg).To(ContainSubstring("Change role"))
		Expect(msg).To(ContainSubstring("RemoveReplica"))
		Expect(msg).NotTo(ContainSubstring("confirmed"))
	})
})
