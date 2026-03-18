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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Migration: RVR does not delete preexisting DRBDR", Label(fw.LabelUpgrade), func() {
	It("DRBDR and LLV survive RVR create and delete", func(ctx SpecContext) {
		placement := f.Discovery.AnyDiskfulPlacement()

		base := f.UniqueName()
		rvrName := v1alpha1.FormatReplicatedVolumeReplicaName(base, 0)

		tllv := f.TestLLV()
		b := tllv.ActualLVName(tllv.Name()).
			LVMVolumeGroupName(placement.LVGName).
			Type("Thin").
			Size("10Mi")
		if placement.ThinPoolName != "" {
			b = b.ThinPoolName(placement.ThinPoolName)
		}
		b.Create(ctx)

		tdrbdr := f.TestDRBDRExact(rvrName).
			Node(placement.NodeName).
			Type(v1alpha1.DRBDResourceTypeDiskful).
			Size("1Mi").
			LVMLogicalVolumeName(tllv.Name()).
			SystemNetworks("Internal").
			NodeID(0).
			Maintenance(v1alpha1.MaintenanceModeNoResourceReconciliation)
		tdrbdr.Create(ctx)
		tdrbdr.Await(ctx, DRBDR.HasAddresses())

		trvr := f.TestRVRExact(base, 0).
			Node(placement.NodeName).
			Type(v1alpha1.ReplicaTypeDiskful).
			LVG(placement.LVGName)
		if placement.ThinPoolName != "" {
			trvr = trvr.ThinPool(placement.ThinPoolName)
		}
		trvr.Create(ctx)

		trvr.Await(ctx, ConditionReason(
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume))

		Expect(tdrbdr.IsPresent()).To(BeTrue(), "DRBDR must not be deleted after RVR creation")
		Expect(tdrbdr.Object().DeletionTimestamp).To(BeNil(), "DRBDR must not have deletionTimestamp")
		Expect(tllv.IsPresent()).To(BeTrue(), "LLV must not be deleted after RVR creation")
		Expect(tllv.Object().DeletionTimestamp).To(BeNil(), "LLV must not have deletionTimestamp")

		rvrObj := trvr.Object()
		tdrbdr.Update(ctx, func(d *v1alpha1.DRBDResource) {
			Expect(controllerutil.SetControllerReference(rvrObj, d, f.Scheme)).To(Succeed())
		})
		tllv.Update(ctx, func(llv *snc.LVMLogicalVolume) {
			Expect(controllerutil.SetControllerReference(rvrObj, llv, f.Scheme)).To(Succeed())
		})

		trvr.Delete(ctx)
		trvr.Await(ctx, Deleted())

		tdrbdr.Await(ctx, Deleted())
		tllv.Await(ctx, Deleted())
	})
})
