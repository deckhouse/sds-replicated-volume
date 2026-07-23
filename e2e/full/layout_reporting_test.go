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
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// E2E-4 — a layout divergence outside the convergence whitelist must be
// reported honestly (reason + exact arithmetic) and must NOT trigger any action
// (block 1). r2->r3 upsize is the negative case for the future US-2.4.
var _ = Describe("Layout: unsupported divergence is reported, not acted upon",
	Label(fw.LabelSlow), Label(fw.LabelFeatureStatus), func() {

		It("reports TransitionUnsupported for an r2->r3 upsize and leaves the layout intact",
			SpecTimeout(10*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating an r2 storage class and a 2D+1TB volume")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationAvailability)

				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal("2D+1TB"))

				rvrCountBefore := trv.RVRCount()

				By("editing rsc.spec.replication to ConsistencyAndAvailability (upsize, out of whitelist)")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationConsistencyAndAvailability //nolint:staticcheck // deliberate unsupported edit
				})

				By("observing LayoutConverged=False/TransitionUnsupported with the exact arithmetic")
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
				trv.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType, "False"))
				trv.Await(ctx, tkmatch.ConditionMessageContains(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType, "have 2D+1TB, want 3D"))

				By("verifying the replica composition is untouched (no new RVR / diskful)")
				Expect(layoutOf(trv)).To(Equal("2D+1TB"))
				Expect(trv.RVRCount()).To(Equal(rvrCountBefore))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))

				By("verifying the volume stays healthy and serving I/O despite the mismatch")
				trv.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedVolumeCondIOReadyType, "True"))

				By("verifying the RSC aggregate is honestly not rolled out")
				trsc.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, "False"))
				trsc.Await(ctx, match.RSC.VolumesAligned(0))

				By("reverting to Availability and observing LayoutConverged recover to Converged")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // revert
				})
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal("2D+1TB"))
			})
	})
