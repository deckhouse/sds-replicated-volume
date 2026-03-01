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

var _ = Describe("Registry", func() {
	It("registers a global transition with a global plan", func() {
		reg := NewRegistry()
		changeQuorum := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum)
		changeQuorum.Plan("qmr-up", v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum).
			GlobalStep("qmr↑", stubGlobalApply, stubGlobalConfirm).
			Build()

		p := reg.get(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum, "qmr-up")
		Expect(p).NotTo(BeNil())
		Expect(p.scope).To(Equal(GlobalScope))
		Expect(p.steps).To(HaveLen(1))
		Expect(p.steps[0].name).To(Equal("qmr↑"))
	})

	It("registers a replica transition with a replica plan", func() {
		reg := NewRegistry()
		addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)
		addReplica.Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
			ReplicaType(v1alpha1.ReplicaTypeAccess).
			ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).
			Build()

		p := reg.get(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "access")
		Expect(p).NotTo(BeNil())
		Expect(p.scope).To(Equal(ReplicaScope))
		Expect(p.replicaType).To(Equal(v1alpha1.ReplicaTypeAccess))
	})

	It("tracks registered transition types", func() {
		reg := NewRegistry()
		reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica).
			Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
			ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).
			Build()

		Expect(reg.hasTransitionType(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)).To(BeTrue())
		Expect(reg.hasTransitionType(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica)).To(BeFalse())
	})

	It("panics on duplicate (transitionType, PlanID)", func() {
		reg := NewRegistry()
		addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)
		addReplica.Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
			ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).Build()

		Expect(func() {
			addReplica.Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
				ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).Build()
		}).To(Panic())
	})

	It("panics on empty PlanID", func() {
		reg := NewRegistry()
		addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)
		Expect(func() {
			addReplica.Plan("", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
				ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).Build()
		}).To(Panic())
	})

	It("panics when no steps added", func() {
		reg := NewRegistry()
		addReplica := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica)
		Expect(func() {
			addReplica.Plan("empty", v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).Build()
		}).To(Panic())
	})
})
