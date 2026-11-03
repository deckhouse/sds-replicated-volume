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

package utils //nolint:revive

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("ReleaseOrphanRVAs", func() {
	const (
		driverName = "replicated.csi.storage.deckhouse.io"
		volumeName = "pvc-orphan"
		nodeName   = "node-1"
	)

	var (
		cl      client.Client
		log     logger.Logger
		traceID string
	)

	BeforeEach(func() {
		cl = newFakeClient()
		log, _ = logger.NewLoggerToWriter(GinkgoWriter, logger.TraceLevel)
		traceID = "test-trace-id"
	})

	rvaExists := func(ctx SpecContext, volume, node string) bool {
		err := cl.Get(ctx, client.ObjectKey{Name: v1alpha1.FormatReplicatedVolumeAttachmentName(volume, node)}, &v1alpha1.ReplicatedVolumeAttachment{})
		if kerrors.IsNotFound(err) {
			return false
		}
		Expect(err).NotTo(HaveOccurred())
		return true
	}

	createVA := func(ctx SpecContext, pvName, node string) {
		Expect(cl.Create(ctx, &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "csi-" + pvName + "-" + node},
			Spec: storagev1.VolumeAttachmentSpec{
				Attacher: driverName,
				NodeName: node,
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: ptr.To(pvName)},
			},
		})).To(Succeed())
	}

	It("deletes an RVA that has no VolumeAttachment and leaves other volumes alone", func(ctx SpecContext) {
		_, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		_, err = EnsureRVA(ctx, cl, log, traceID, "pvc-other", nodeName)
		Expect(err).NotTo(HaveOccurred())

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeFalse())
		Expect(rvaExists(ctx, "pvc-other", nodeName)).To(BeTrue())
		Expect(CheckNoRVAsExist(ctx, cl, log, traceID, volumeName)).To(Succeed())
	})

	It("keeps an RVA that a VolumeAttachment of this driver still refers to", func(ctx SpecContext) {
		_, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		createVA(ctx, volumeName, nodeName)

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeTrue())
	})

	It("matches a VolumeAttachment through a PersistentVolume whose name differs from the volume ID", func(ctx SpecContext) {
		_, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Create(ctx, &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "static-pv"},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{Driver: driverName, VolumeHandle: volumeName},
				},
			},
		})).To(Succeed())
		createVA(ctx, "static-pv", nodeName)

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeTrue())
	})

	It("ignores a VolumeAttachment of another node or another driver", func(ctx SpecContext) {
		_, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		createVA(ctx, volumeName, "node-2")
		Expect(cl.Create(ctx, &storagev1.VolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "foreign"},
			Spec: storagev1.VolumeAttachmentSpec{
				Attacher: "other.csi.example.com",
				NodeName: nodeName,
				Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: ptr.To(volumeName)},
			},
		})).To(Succeed())

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeFalse())
	})

	It("keeps an RVA without a VolumeAttachment while its device is in use", func(ctx SpecContext) {
		rvaName, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, rva)).To(Succeed())
		rva.Status.InUse = ptr.To(true)
		Expect(cl.Status().Update(ctx, rva)).To(Succeed())

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeTrue())
	})

	It("finishes an RVA that is already being deleted but holds the csi-controller finalizer", func(ctx SpecContext) {
		rvaName, err := EnsureRVA(ctx, cl, log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, rva)).To(Succeed())
		Expect(rva.Finalizers).To(ContainElement(v1alpha1.CSIControllerFinalizer))
		Expect(cl.Delete(ctx, rva)).To(Succeed())
		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeTrue())

		Expect(ReleaseOrphanRVAs(ctx, cl, log, traceID, driverName, volumeName)).To(Succeed())

		Expect(rvaExists(ctx, volumeName, nodeName)).To(BeFalse())
	})
})
