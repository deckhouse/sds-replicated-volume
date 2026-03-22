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
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Migration: agent adopts preexisting DRBD", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	DescribeTable("adopts and cleans up",
		func(ctx SpecContext, l fw.TestLayout) {
			replicas := f.SetupLayout(ctx, l).EmulatePreexisting(ctx)
			Expect(replicas).To(HaveLen(l.ExpectedReplicas()))

			var llvs []*fw.TestLLV
			var drbdrs []*fw.TestDRBDR
			var primaryDRBDR *fw.TestDRBDR

			for _, r := range replicas {
				var drbdType v1alpha1.DRBDResourceType
				var tllv *fw.TestLLV

				switch r.Type {
				case v1alpha1.ReplicaTypeDiskful:
					drbdType = v1alpha1.DRBDResourceTypeDiskful
					tllv = f.TestLLV()
					b := tllv.ActualLVName(r.ActualLVName).
						LVMVolumeGroupName(r.LVGName).
						Type("Thin").
						Size(r.Size)
					if r.ThinPoolName != "" {
						b = b.ThinPoolName(r.ThinPoolName)
					}
					b.Create(ctx)
					llvs = append(llvs, tllv)
				case v1alpha1.ReplicaTypeTieBreaker:
					drbdType = v1alpha1.DRBDResourceTypeDiskless
				default:
					continue
				}

				db := f.TestDRBDR().
					Node(r.NodeName).
					Type(drbdType).
					SystemNetworks("Internal").
					NodeID(r.NodeID).
					ActualNameOnTheNode(r.DRBDName).
					Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
				if drbdType == v1alpha1.DRBDResourceTypeDiskful {
					db = db.Size(r.Size).LVMLogicalVolumeName(tllv.Name())
				}
				if r.Role == v1alpha1.DRBDRolePrimary {
					db = db.Role(v1alpha1.DRBDRolePrimary)
				}
				db.Create(ctx)
				drbdrs = append(drbdrs, db)

				if r.Role == v1alpha1.DRBDRolePrimary {
					primaryDRBDR = db
				}
			}

			for _, td := range drbdrs {
				td.Await(ctx, DRBDR.HasAddresses())
			}

			originalAddrs := make(map[uint8][]v1alpha1.DRBDResourceAddressStatus, len(replicas))
			for _, r := range replicas {
				originalAddrs[r.NodeID] = r.Addresses
			}
			multiReplica := len(replicas) > 1
			for _, td := range drbdrs {
				obj := td.Object()
				assertAddressesAdopted(obj.Status.Addresses, originalAddrs[obj.Spec.NodeID],
					multiReplica, td.Name())
			}

			for _, td := range drbdrs {
				obj := td.Object()
				if obj.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
					td.Await(ctx, DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
				}
			}

			swUpToDate := NewSwitch(DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
			for _, td := range drbdrs {
				obj := td.Object()
				if obj.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
					td.Always(swUpToDate)
				}
			}
			var swPrimaryDevice *Switch
			if primaryDRBDR != nil {
				swPrimaryDevice = NewSwitch(And(DRBDR.HasDevice(), Not(DRBDR.IOSuspended())))
				primaryDRBDR.Always(swPrimaryDevice)
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
			if swPrimaryDevice != nil {
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

		Entry("FTT=0 GMDR=0 (1D)",
			fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("FTT=0 GMDR=0 (1D, 1att)",
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),

		Entry("FTT=0 GMDR=1 (2D)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("FTT=0 GMDR=1 (2D, 1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}),

		Entry("FTT=1 GMDR=0 (2D+1TB)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("FTT=1 GMDR=0 (2D+1TB, 1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}),

		Entry("FTT=1 GMDR=1 (3D)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("FTT=1 GMDR=1 (3D, 1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}),
	)
})
