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
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Migration: agent adopts preexisting DRBD", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	DescribeTable("adopts and cleans up",
		func(ctx SpecContext, ftt, gmdr byte, expectedReplicas int, wantPrimary bool) {
			replicas := createPreexistingDRBD(ctx, ftt, gmdr)
			Expect(replicas).To(HaveLen(expectedReplicas))

			primaryIdx := -1
			if wantPrimary && len(replicas) > 0 {
				primaryIdx = rand.Intn(len(replicas))
				res := f.Drbdsetup(ctx, replicas[primaryIdx].NodeName, "primary", replicas[primaryIdx].DRBDName)
				Expect(res.ExitCode).To(Equal(0))
			}

			llvs := make([]*fw.TestLLV, len(replicas))
			for i, r := range replicas {
				tllv := f.TestLLV()
				b := tllv.ActualLVName(r.ActualLVName).
					LVMVolumeGroupName(r.LVGName).
					Type("Thin").
					Size(r.Size)
				if r.ThinPoolName != "" {
					b = b.ThinPoolName(r.ThinPoolName)
				}
				b.Create(ctx)
				llvs[i] = tllv
			}

			drbdrs := make([]*fw.TestDRBDR, len(replicas))
			for i, r := range replicas {
				b := f.TestDRBDR().
					Node(r.NodeName).
					Type(v1alpha1.DRBDResourceTypeDiskful).
					Size(r.Size).
					LVMLogicalVolumeName(llvs[i].Name()).
					SystemNetworks("Internal").
					NodeID(r.NodeID).
					ActualNameOnTheNode(r.DRBDName).
					Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
				if wantPrimary && i == primaryIdx {
					b = b.Role(v1alpha1.DRBDRolePrimary)
				}
				b.Create(ctx)
				drbdrs[i] = b
			}

			for _, td := range drbdrs {
				td.Await(ctx, And(
					DRBDR.HasAddresses(),
					DRBDR.DiskState(v1alpha1.DiskStateUpToDate)))
			}

			swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
			for _, td := range drbdrs {
				td.Always(swUpToDate)
			}
			var swPrimaryDevice *Switch
			if wantPrimary {
				swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
				drbdrs[primaryIdx].Always(swPrimaryDevice)
			}

			for i, td := range drbdrs {
				var peers []v1alpha1.DRBDResourcePeer
				for j, other := range drbdrs {
					if j == i {
						continue
					}
					otherObj := other.Object()
					var paths []v1alpha1.DRBDResourcePath
					for _, addr := range otherObj.Status.Addresses {
						paths = append(paths, v1alpha1.DRBDResourcePath(addr))
					}
					peers = append(peers, v1alpha1.DRBDResourcePeer{
						Name:   otherObj.Spec.NodeName,
						NodeID: otherObj.Spec.NodeID,
						Paths:  paths,
					})
				}
				if len(peers) > 0 {
					td.Update(ctx, func(d *v1alpha1.DRBDResource) {
						d.Spec.Peers = peers
					})
				}
			}

			for _, td := range drbdrs {
				td.Update(ctx, func(d *v1alpha1.DRBDResource) {
					d.Spec.Maintenance = ""
				})
			}

			for _, td := range drbdrs {
				td.Await(ctx, ConditionStatus(
					v1alpha1.DRBDResourceCondConfiguredType, "True"))
			}

			swUpToDate.Disable()
			if wantPrimary {
				swPrimaryDevice.Disable()
			}

			for _, td := range drbdrs {
				td.Delete(ctx)
				td.Await(ctx, Deleted())
			}
			for _, tl := range llvs {
				tl.Delete(ctx)
				tl.Await(ctx, Deleted())
			}
		},

		Entry("FTT=0 GMDR=0 secondary (1 replica)",
			byte(0), byte(0), 1, false),
		Entry("FTT=0 GMDR=0 primary (1 replica)",
			byte(0), byte(0), 1, true),

		Entry("FTT=0 GMDR=1 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, false),
		Entry("FTT=0 GMDR=1 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(0), byte(1), 2, true),

		Entry("FTT=1 GMDR=0 secondary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, false),
		Entry("FTT=1 GMDR=0 primary (2 replicas)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			byte(1), byte(0), 2, true),

		Entry("FTT=1 GMDR=1 secondary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, false),
		Entry("FTT=1 GMDR=1 primary (3 replicas)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			byte(1), byte(1), 3, true),
	)
})
