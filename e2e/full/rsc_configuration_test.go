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

package full

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RSC configuration", func() {
	Context("controller-side defaulting", func() {
		It("fills defaults for optional fields when not specified", func(ctx SpecContext) {
			trsc := f.TestRSC().
				StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
				StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				FTT(0).GMDR(0)
			trsc.Create(ctx)

			trsc.Await(ctx, ConditionStatus(
				v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

			rsc := trsc.Object()
			Expect(rsc.Spec.SystemNetworkNames).To(Equal([]string{"Internal"}))
			Expect(rsc.Spec.ConfigurationRolloutStrategy).NotTo(BeNil())
			Expect(rsc.Spec.ConfigurationRolloutStrategy.Type).To(Equal(v1alpha1.ConfigurationRolloutRollingUpdate))
			Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate).NotTo(BeNil())
			Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate.MaxParallel).To(Equal(int32(5)))
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy).NotTo(BeNil())
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.Type).To(Equal(v1alpha1.EligibleNodesConflictResolutionRollingRepair))
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair).NotTo(BeNil())
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair.MaxParallel).To(Equal(int32(5)))
			Expect(rsc.Spec.EligibleNodesPolicy).NotTo(BeNil())
			Expect(rsc.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration).To(Equal(10 * time.Minute))
		})

		It("preserves explicitly set strategies", func(ctx SpecContext) {
			trsc := f.TestRSC().
				StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
				StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				FTT(0).GMDR(0).
				ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutNewVolumesOnly).
				EligibleNodesConflictResolutionStrategyType(v1alpha1.EligibleNodesConflictResolutionManual)
			trsc.Create(ctx)

			trsc.Await(ctx, ConditionStatus(
				v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

			rsc := trsc.Object()
			Expect(rsc.Spec.ConfigurationRolloutStrategy.Type).To(Equal(v1alpha1.ConfigurationRolloutNewVolumesOnly))
			Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate).To(BeNil())
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.Type).To(Equal(v1alpha1.EligibleNodesConflictResolutionManual))
			Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair).To(BeNil())
		})
	})

	Context("API validation", func() {
		It("rejects creation when spec.storage is missing", func(ctx SpecContext) {
			trsc := f.TestRSC().
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored)
			trsc.CreateExpect(ctx, MatchError(ContainSubstring("spec.storage is required")))
		})

		It("rejects strategy with type RollingUpdate but no rollingUpdate", func(ctx SpecContext) {
			trsc := f.TestRSC().
				StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
				StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				FTT(0).GMDR(0).
				ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutRollingUpdate)
			trsc.CreateExpect(ctx, MatchError(ContainSubstring("rollingUpdate is required")))
		})

		It("accepts strategy with type NewVolumesOnly", func(ctx SpecContext) {
			trsc := f.TestRSC().
				StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
				StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				FTT(0).GMDR(0).
				ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutNewVolumesOnly)
			trsc.Create(ctx)
		})
	})
})
