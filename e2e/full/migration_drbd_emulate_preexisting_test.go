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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// PreexistingDRBDReplica holds information about a DRBD resource that
// simulates a preexisting replica from a previous control plane.
type PreexistingDRBDReplica struct {
	NodeName     string
	DRBDName     string
	NodeID       uint8
	ActualLVName string
	ActualVGName string
	LVGName      string
	ThinPoolName string
	Size         string
}

// createPreexistingDRBD creates an RV with the given FTT/GMDR, waits for
// formation, renames all DRBD resources on the nodes (simulating preexisting
// resources from a previous control plane), deletes the RV, and returns the
// replica info. A DeferCleanup is always registered that checks whether
// DRBD/LVM resources still exist before cleaning them up.
func createPreexistingDRBD(ctx SpecContext, ftt, gmdr byte) []PreexistingDRBDReplica {
	trv := f.TestRV().FTT(ftt).GMDR(gmdr)
	trv.Create(ctx)
	trv.Await(ctx, RV.FormationComplete())

	type memberInfo struct {
		nodeName     string
		oldDRBDName  string
		newDRBDName  string
		nodeID       uint8
		drbdr        *fw.TestDRBDR
		llv          *fw.TestLLV
		lvDevPath    string
		actualLVName string
		actualVGName string
		lvgName      string
		thinPoolName string
		size         string
	}

	count := trv.RVRCount()
	members := make([]memberInfo, count)
	for i := range members {
		trvr := trv.TestRVR(i)
		drbdr := trvr.DRBDR()

		llvs := trvr.LLVs()
		Expect(llvs).To(HaveLen(1), "expected exactly 1 LLV for diskful RVR %d", i)
		tllv := llvs[0]
		llvObj := tllv.Object()

		var lvg snc.LVMVolumeGroup
		Expect(f.Client.Get(ctx, client.ObjectKey{Name: llvObj.Spec.LVMVolumeGroupName}, &lvg)).To(Succeed())
		lvDevPath := "/dev/" + lvg.Spec.ActualVGNameOnTheNode + "/" + llvObj.Spec.ActualLVNameOnTheNode

		var thinPoolName string
		if llvObj.Spec.Thin != nil {
			thinPoolName = llvObj.Spec.Thin.PoolName
		}

		drbdrObj := drbdr.Object()
		members[i] = memberInfo{
			nodeName:     drbdrObj.Spec.NodeName,
			oldDRBDName:  "sdsrv-" + drbdr.Name(),
			newDRBDName:  f.UniqueName(),
			nodeID:       drbdrObj.Spec.NodeID,
			drbdr:        drbdr,
			llv:          tllv,
			lvDevPath:    lvDevPath,
			actualLVName: llvObj.Spec.ActualLVNameOnTheNode,
			actualVGName: lvg.Spec.ActualVGNameOnTheNode,
			lvgName:      llvObj.Spec.LVMVolumeGroupName,
			thinPoolName: thinPoolName,
			size:         llvObj.Spec.Size,
		}
	}

	DeferCleanup(func(cleanupCtx context.Context) {
		fmt.Fprintln(GinkgoWriter, "[cleanup] preexisting DRBD: tearing down DRBD resources and LVs")
		for _, m := range members {
			res := f.Drbdsetup(cleanupCtx, m.nodeName, "status", m.newDRBDName)
			if res.ExitCode == 0 {
				f.Drbdsetup(cleanupCtx, m.nodeName, "down", m.newDRBDName)
				Eventually(func() int {
					return f.Drbdsetup(cleanupCtx, m.nodeName, "status", m.newDRBDName).ExitCode
				}).WithContext(cleanupCtx).Should(Not(Equal(0)))
			}
		}
		for _, m := range members {
			res := f.LVM(cleanupCtx, m.nodeName, "lvs", m.lvDevPath)
			if res.ExitCode == 0 {
				f.LVM(cleanupCtx, m.nodeName, "lvremove", "-f", m.lvDevPath)
			}
		}
		fmt.Fprintln(GinkgoWriter, "[cleanup] preexisting DRBD: done")
	})

	for i := range members {
		members[i].drbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		})
	}
	for _, m := range members {
		m.drbdr.Await(ctx, ConditionReason(
			v1alpha1.DRBDResourceCondConfiguredType,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance))
	}

	for _, m := range members {
		res := f.Drbdsetup(ctx, m.nodeName, "rename-resource", m.oldDRBDName, m.newDRBDName)
		Expect(res.ExitCode).To(Equal(0))
	}

	trv.Delete(ctx)

	for _, m := range members {
		if m.llv.IsPresent() {
			m.llv.Update(ctx, func(llv *snc.LVMLogicalVolume) {
				llv.SetFinalizers(nil)
			})
		}
	}

	trv.Await(ctx, Deleted())

	result := make([]PreexistingDRBDReplica, count)
	for i, m := range members {
		result[i] = PreexistingDRBDReplica{
			NodeName:     m.nodeName,
			DRBDName:     m.newDRBDName,
			NodeID:       m.nodeID,
			ActualLVName: m.actualLVName,
			ActualVGName: m.actualVGName,
			LVGName:      m.lvgName,
			ThinPoolName: m.thinPoolName,
			Size:         m.size,
		}
	}
	return result
}

var _ = Describe("Preexisting DRBD emulation", Label(fw.LabelUpgrade), func() {
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
			f.Drbdsetup(cleanupCtx, node, "down", newDRBDName)
		})

		tdrbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		})
		tdrbdr.Await(ctx, ConditionReason(
			v1alpha1.DRBDResourceCondConfiguredType,
			v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance))

		res := f.Drbdsetup(ctx, node, "rename-resource", oldDRBDName, newDRBDName)
		Expect(res.ExitCode).To(Equal(0))

		tdrbdr.Delete(ctx)
		tdrbdr.Await(ctx, Deleted())

		res = f.Drbdsetup(ctx, node, "status", newDRBDName)
		Expect(res.ExitCode).To(Equal(0))
	})

	It("createPreexistingDRBD produces 3 UpToDate replicas for FTT=1 GMDR=1",
		require.MinNodes(3), func(ctx SpecContext) {
			replicas := createPreexistingDRBD(ctx, 1, 1)
			Expect(replicas).To(HaveLen(3))

			for _, r := range replicas {
				res := f.Drbdsetup(ctx, r.NodeName, "show", r.DRBDName)
				Expect(res.ExitCode).To(Equal(0))
			}

			for _, r := range replicas {
				res := f.Drbdsetup(ctx, r.NodeName, "status", r.DRBDName)
				Expect(res.ExitCode).To(Equal(0))
				Expect(res.Stdout).To(ContainSubstring("UpToDate"))
			}
		})
})
