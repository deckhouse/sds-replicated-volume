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
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// E2E-NVO — configurationRolloutStrategy=NewVolumesOnly.
//
// The strategy used to be inert: an edited storage class rolled out to every
// volume regardless. Now an existing volume HOLDS its configuration — and says
// so, instead of pretending to be up to date — while volumes created after the
// edit get the new one. Switching the strategy back to RollingUpdate releases
// the held volumes.
var _ = Describe("Layout: NewVolumesOnly holds existing volumes",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		It("holds the old volume at 3D, creates new ones as 2D+1TB, and releases the hold on RollingUpdate",
			SpecTimeout(25*time.Minute), require.MinNodes(3), func(ctx SpecContext) {
				By("creating an r3 storage class that only configures new volumes")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationConsistencyAndAvailability,
					func(rsc *fw.TestRSC) {
						rsc.ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutNewVolumesOnly)
					})

				By("creating a 3D volume before the edit")
				oldRV := f.TestRV().RSCName(trsc.Name())
				oldRV.Create(ctx)
				oldRV.Await(ctx, match.RV.FormationComplete())
				oldRV.Await(ctx, match.RV.Members(3))
				oldRV.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(oldRV)).To(Equal(ptr.To("3D")))

				By("editing rsc.spec.replication to Availability")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("the old volume reports the newer configuration as held")
				oldRV.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
					v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld))
				oldRV.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedVolumeCondConfigurationReadyType, "False"))

				// The hold must last, not merely be reported once: the invariant
				// runs on every snapshot that arrives while the second volume is
				// formed below, which is a real stretch of cluster time.
				held := tkmatch.NewSwitch(heldAt3D())
				oldRV.Always(held)

				By("the storage class reports the rollout as disabled, with the volume counted stale")
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))
				trsc.Await(ctx, match.RSC.VolumesStale(1))

				By("a volume created after the edit gets the new configuration")
				newRV := f.TestRV().RSCName(trsc.Name())
				newRV.Create(ctx)
				newRV.Await(ctx, match.RV.FormationComplete())
				newRV.Await(ctx, match.RV.Members(3))
				newRV.Await(ctx, migratedToR2())
				Expect(memberTypeCount(newRV, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				newRV.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
					v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))

				By("the old volume is still held at 3D")
				Expect(membershipLayoutOf(oldRV)).To(Equal(ptr.To("3D")))
				Expect(memberTypeCount(oldRV, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(3))

				By("switching the strategy to RollingUpdate")
				held.Disable()
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.ConfigurationRolloutStrategy = &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
						Type: v1alpha1.ConfigurationRolloutRollingUpdate,
						RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
							MaxParallel: 5,
						},
					}
				})

				By("the held volume migrates to 2D+1TB")
				oldRV.Await(ctx, migratedToR2())
				Expect(memberTypeCount(oldRV, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				oldRV.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
					v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))

				By("the storage class reports both volumes aligned")
				trsc.Await(ctx, match.RSC.VolumesAligned(2))
				trsc.Await(ctx, match.RSC.VolumesStale(0))
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
			})
	})

// heldAt3D matches a volume that is still on its own 3D layout and still says
// the newer storage class configuration is deliberately not applied.
func heldAt3D() types.GomegaMatcher {
	return match.RV.Custom("held at 3D", func(rv *v1alpha1.ReplicatedVolume) bool {
		if rv.Status.MembershipLayout == nil || *rv.Status.MembershipLayout != "3D" {
			return false
		}
		for i := range rv.Status.Conditions {
			c := &rv.Status.Conditions[i]
			if c.Type == v1alpha1.ReplicatedVolumeCondConfigurationReadyType {
				return c.Status == metav1.ConditionFalse &&
					c.Reason == v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld
			}
		}
		return false
	})
}
