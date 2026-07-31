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
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// E2E-7 — the conservative r3->r2 update matrix (block 4): incompatible spec
// changes are rejected with a field-naming error while replication edits pass.
var _ = Describe("Layout: incompatible ReplicatedStorageClass updates are rejected",
	Label(fw.LabelSlow), func() {

		It("rejects storage/topology changes and accepts a replication edit",
			SpecTimeout(10*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating an r2 storage class with an existing volume")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationAvailability)

				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				By("rejecting a storage type change (answered by the CEL consistency guard)")
				// CRD validation (CEL) runs BEFORE validating webhooks. Flipping type
				// to LVM while the lvmVolumeGroups entries still carry thinPoolName
				// violates the cross-field CEL rule on spec.storage, so this probe is
				// rejected by CEL — the webhook's immutability guard never gets a say.
				trsc.UpdateExpect(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Storage.Type = v1alpha1.ReplicatedStoragePoolTypeLVM
				}, MatchError(ContainSubstring("thinPoolName must not be specified when type is LVM")))

				By("rejecting a storage composition change (immutable once set, webhook)")
				// A mutation that no consistency rule claims first: changing an
				// entry's thinPoolName keeps the object schema-valid and consistent,
				// so the rejection can only come from the update webhook's
				// "immutable once set" guard on spec.storage.
				trsc.UpdateExpect(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Storage.LVMVolumeGroups[0].ThinPoolName = "e2e-bogus-pool"
				}, MatchError(ContainSubstring("spec.storage is immutable once set")))

				By("rejecting a topology change (immutable)")
				trsc.UpdateExpect(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Topology = v1alpha1.TopologyZonal
				}, MatchError(ContainSubstring("spec.topology is immutable")))

				By("verifying the RSC spec and the volume layout are untouched")
				Expect(trsc.Object().Spec.Topology).To(Equal(v1alpha1.TopologyIgnored))
				Expect(trsc.Object().Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVMThin))
				for _, g := range trsc.Object().Spec.Storage.LVMVolumeGroups {
					Expect(g.ThinPoolName).NotTo(Equal("e2e-bogus-pool"))
				}
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				By("accepting a legitimate replication edit")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationConsistencyAndAvailability //nolint:staticcheck // replication is mutable per the migration matrix
				})
				//nolint:staticcheck // asserting the mutable deprecated field took effect
				Expect(trsc.Object().Spec.Replication).To(Equal(v1alpha1.ReplicationConsistencyAndAvailability))
			})
	})
