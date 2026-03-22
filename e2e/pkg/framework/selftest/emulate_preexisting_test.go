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

package selftest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("EmulatePreexisting", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	It("standalone DRBDR: DRBD resource persists after rename + maintenance + delete", func(ctx SpecContext) {
		node := f.Discovery.AnyNode()

		tdrbdr := f.TestDRBDR().
			Node(node).
			Type(v1alpha1.DRBDResourceTypeDiskless).
			SystemNetworks("Internal").
			NodeID(0)
		tdrbdr.Create(ctx)

		tdrbdr.Await(ctx, ConditionStatus(
			v1alpha1.DRBDResourceCondConfiguredType, "True"))

		drbdrName := tdrbdr.Name()
		oldDRBDName := "sdsrv-" + drbdrName
		newDRBDName := f.UniqueName()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = f.Drbdsetup(cleanupCtx, node, "down", newDRBDName)
		})

		tdrbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		})
		tdrbdr.Await(ctx, ConditionReason(
			v1alpha1.DRBDResourceCondConfiguredType,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance))

		res, err := f.Drbdsetup(ctx, node, "rename-resource", oldDRBDName, newDRBDName)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ExitCode).To(Equal(0))

		tdrbdr.Delete(ctx)
		tdrbdr.Await(ctx, Deleted())

		res, err = f.Drbdsetup(ctx, node, "status", newDRBDName)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ExitCode).To(Equal(0))
	})

	DescribeTable("SetupLayout + EmulatePreexisting produces correct preexisting state",
		func(ctx SpecContext, l fw.TestLayout) {
			trv := f.SetupLayout(ctx, l)
			replicas := trv.EmulatePreexisting(ctx)
			Expect(replicas).To(HaveLen(l.ExpectedReplicas()))

			D := int(l.FTT) + int(l.GMDR) + 1
			wantTB := 0
			if D%2 == 0 && D > 0 && int(l.FTT) == D/2 {
				wantTB = 1
			}

			var gotD, gotTB, gotA, gotPrimary int
			for _, r := range replicas {
				switch r.Type {
				case v1alpha1.ReplicaTypeDiskful:
					gotD++
				case v1alpha1.ReplicaTypeTieBreaker:
					gotTB++
				case v1alpha1.ReplicaTypeAccess:
					gotA++
				}
				if r.Role == v1alpha1.DRBDRolePrimary {
					gotPrimary++
				} else {
					Expect(r.Role).To(Equal(v1alpha1.DRBDRoleSecondary))
				}
			}
			Expect(gotD).To(Equal(D), "diskful count")
			Expect(gotTB).To(Equal(wantTB), "tiebreaker count")
			Expect(gotA).To(Equal(l.Access), "access count")
			Expect(gotPrimary).To(Equal(l.Attached), "attached/primary count")

			for _, r := range replicas {
				res, err := f.Drbdsetup(ctx, r.NodeName, "status", r.DRBDName)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.ExitCode).To(Equal(0), "DRBD resource %s must exist on %s", r.DRBDName, r.NodeName)

				if r.Type == v1alpha1.ReplicaTypeDiskful {
					Expect(res.Stdout).To(ContainSubstring("UpToDate"), "diskful %s must be UpToDate", r.DRBDName)
				}
				if r.Role == v1alpha1.DRBDRolePrimary {
					Expect(res.Stdout).To(ContainSubstring("Primary"), "primary %s must show Primary", r.DRBDName)
				}
			}
		},

		// --- 1D ---
		Entry("1D",
			fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("1D (1att)",
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),
		Entry("1D+1A (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}),
		Entry("1D+1A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}),
		Entry("1D+2A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}),

		// --- 2D ---
		Entry("2D",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}),
		Entry("2D (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}),
		Entry("2D+1A (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}),
		Entry("2D+1A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}),
		Entry("2D+2A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 2, Attached: 2}),

		// --- 2D+1TB ---
		Entry("2D+1TB",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D+1TB (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}),
		Entry("2D+1TB (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 2}),
		Entry("2D+1TB+1A (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 1}),
		Entry("2D+1TB+1A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 2}),
		Entry("2D+1TB+2A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 2, Attached: 2}),

		// --- 3D ---
		Entry("3D",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("3D (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}),
		Entry("3D (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}),
		Entry("3D+1A (1att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 1}),
		Entry("3D+1A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}),
		Entry("3D+2A (2att)",
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 2, Attached: 2}),
	)
})
