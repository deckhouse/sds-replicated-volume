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
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("SC lifecycle", func() {
	It("creates SC when RSC is created", func(ctx SpecContext) {
		trsc := f.TestRSC().
			StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
			StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
			ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
			VolumeAccess(v1alpha1.VolumeAccessAny).
			Topology(v1alpha1.TopologyIgnored).
			FTT(0).GMDR(0)
		trsc.Create(ctx)

		trsc.Await(ctx, tkmatch.ConditionStatus(
			v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

		tsc := trsc.TestSC()
		tsc.Await(ctx, tkmatch.Present())

		sc := tsc.Object()
		Expect(sc.Provisioner).To(Equal(v1alpha1.CSIProvisioner))
		Expect(sc.Parameters).To(HaveKeyWithValue(v1alpha1.CSIParamRSCNameKey, trsc.Name()))
		Expect(sc.ReclaimPolicy).NotTo(BeNil())
		Expect(*sc.ReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimDelete))
		Expect(sc.VolumeBindingMode).NotTo(BeNil())
		Expect(*sc.VolumeBindingMode).To(Equal(storagev1.VolumeBindingImmediate))
		Expect(sc.AllowVolumeExpansion).NotTo(BeNil())
		Expect(*sc.AllowVolumeExpansion).To(BeTrue())
		Expect(sc.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByLabelKey, v1alpha1.ManagedByLabelValue))
		Expect(sc.Finalizers).To(ContainElement(v1alpha1.StorageClassFinalizer))
	})

	It("deletes SC when RSC is deleted", func(ctx SpecContext) {
		trsc := f.TestRSC().
			StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
			StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
			ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
			Topology(v1alpha1.TopologyIgnored).
			FTT(0).GMDR(0)
		trsc.Create(ctx)

		trsc.Await(ctx, tkmatch.ConditionStatus(
			v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

		tsc := trsc.TestSC()
		tsc.Await(ctx, tkmatch.Present())

		trsc.Delete(ctx)
		trsc.Await(ctx, tkmatch.Deleted())

		tsc.Await(ctx, tkmatch.Deleted())
	})

	It("recreates SC when RSC reclaimPolicy changes", func(ctx SpecContext) {
		trsc := f.TestRSC().
			StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
			StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
			ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
			Topology(v1alpha1.TopologyIgnored).
			FTT(0).GMDR(0)
		trsc.Create(ctx)

		trsc.Await(ctx, tkmatch.ConditionStatus(
			v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

		tsc := trsc.TestSC()
		tsc.Await(ctx, tkmatch.Present())

		sc := tsc.Object()
		Expect(*sc.ReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimDelete))

		trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
			rsc.Spec.ReclaimPolicy = v1alpha1.RSCReclaimPolicyRetain
		})

		tsc.Await(ctx, tkmatch.Deleted())
		tsc.Await(ctx, tkmatch.PresentAgain())

		sc = tsc.Object()
		Expect(sc.ReclaimPolicy).NotTo(BeNil())
		Expect(*sc.ReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))
	})

	Context("webhook validation", func() {
		It("rejects RSC when SC with same name and different provisioner exists", func(ctx SpecContext) {
			tsc := f.TestSC("foreign").Provisioner("kubernetes.io/no-provisioner")
			tsc.Create(ctx)

			trsc := f.TestRSCExact(tsc.Name()).
				StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
				StorageLVMVolumeGroups(f.Discovery.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				FTT(0).GMDR(0)
			trsc.CreateExpect(ctx, MatchError(ContainSubstring("already exists with provisioner")))
		})
	})
})
