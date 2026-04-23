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

// TODO: rename package to something meaningful (revive: var-naming).
package utils //nolint:revive

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

func TestPublishUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RVA Utils Suite")
}

var _ = Describe("ReplicatedVolumeAttachment utils", func() {
	var (
		cl      client.Client
		log     logger.Logger
		traceID string
	)

	BeforeEach(func() {
		cl = newFakeClient()
		log = logger.WrapLorg(GinkgoLogr)
		traceID = "test-trace-id"
	})

	It("EnsureRVA creates a new RVA when it does not exist", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		rvaName, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvaName).ToNot(BeEmpty())

		got := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, got)).To(Succeed())
		Expect(got.Spec.ReplicatedVolumeName).To(Equal(volumeName))
		Expect(got.Spec.NodeName).To(Equal(nodeName))
	})

	It("EnsureRVA is idempotent when RVA already exists", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		_, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())
		_, err = EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())

		list := &v1alpha1.ReplicatedVolumeAttachmentList{}
		Expect(cl.List(ctx, list)).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
	})

	It("DeleteRVA deletes existing RVA and is idempotent", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		_, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())

		Expect(DeleteRVA(ctx, cl, &log, traceID, volumeName, nodeName)).To(Succeed())
		Expect(DeleteRVA(ctx, cl, &log, traceID, volumeName, nodeName)).To(Succeed())

		list := &v1alpha1.ReplicatedVolumeAttachmentList{}
		Expect(cl.List(ctx, list)).To(Succeed())
		Expect(list.Items).To(HaveLen(0))
	})

	It("WaitForRVAReady returns nil when Ready=True", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		rvaName, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())

		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, rva)).To(Succeed())
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached,
			Message:            "attached",
			ObservedGeneration: rva.Generation,
		})
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
			Message:            "io ready",
			ObservedGeneration: rva.Generation,
		})
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady,
			Message:            "ok",
			ObservedGeneration: rva.Generation,
		})
		Expect(cl.Status().Update(ctx, rva)).To(Succeed())

		Expect(WaitForRVAReady(ctx, cl, &log, traceID, volumeName, nodeName)).To(Succeed())
	})

	It("WaitForRVAReady returns error immediately when Attached=False and reason=LocalityNotSatisfied", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		rvaName, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())

		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, rva)).To(Succeed())
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied,
			Message:            "Local volume access requires a Diskful replica on the requested node",
			ObservedGeneration: rva.Generation,
		})
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
			Message:            "Waiting for volume to be attached to the requested node",
			ObservedGeneration: rva.Generation,
		})
		Expect(cl.Status().Update(ctx, rva)).To(Succeed())

		start := time.Now()
		err = WaitForRVAReady(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))

		var waitErr *RVAWaitError
		Expect(errors.As(err, &waitErr)).To(BeTrue())
		Expect(waitErr.Permanent).To(BeTrue())
		Expect(waitErr.LastReadyCondition).NotTo(BeNil())
		Expect(waitErr.LastReadyCondition.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached))
		Expect(waitErr.LastAttachedCondition).NotTo(BeNil())
		Expect(waitErr.LastAttachedCondition.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied))
	})

	It("WaitForRVAReady returns context deadline error but includes last observed reason/message", func(ctx SpecContext) {
		volumeName := "test-volume"
		nodeName := "node-1"

		rvaName, err := EnsureRVA(ctx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).NotTo(HaveOccurred())

		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, rva)).To(Succeed())
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching,
			Message:            "Waiting for replica to become Primary",
			ObservedGeneration: rva.Generation,
		})
		meta.SetStatusCondition(&rva.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
			Message:            "Waiting for volume to be attached to the requested node",
			ObservedGeneration: rva.Generation,
		})
		Expect(cl.Status().Update(ctx, rva)).To(Succeed())

		timeoutCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()

		err = WaitForRVAReady(timeoutCtx, cl, &log, traceID, volumeName, nodeName)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

		var waitErr *RVAWaitError
		Expect(errors.As(err, &waitErr)).To(BeTrue())
		Expect(waitErr.LastReadyCondition).NotTo(BeNil())
		Expect(waitErr.LastReadyCondition.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached))
		Expect(waitErr.LastAttachedCondition).NotTo(BeNil())
		Expect(waitErr.LastAttachedCondition.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching))
		Expect(waitErr.LastAttachedCondition.Message).To(Equal("Waiting for replica to become Primary"))
	})
})

var _ = Describe("WaitForRVADeleted", func() {
	var (
		cl      client.Client
		log     logger.Logger
		traceID string
	)

	const stuckFinalizer = "test.deckhouse.io/stuck"

	BeforeEach(func() {
		cl = newFakeClient()
		log = logger.WrapLorg(GinkgoLogr)
		traceID = "test-trace-id"
	})

	Context("when RVA does not exist", func() {
		It("should return immediately without error", func(ctx SpecContext) {
			err := WaitForRVADeleted(ctx, cl, &log, traceID, "missing-volume", "missing-node")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when RVA is deleted in background", func() {
		It("should wait and return successfully once the object is gone", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"
			rvaName := BuildRVAName(volumeName, nodeName)

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rvaName,
					Finalizers: []string{stuckFinalizer, SDSReplicatedVolumeCSIFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: volumeName,
					NodeName:             nodeName,
				},
			}
			Expect(cl.Create(ctx, rva)).To(Succeed())

			// Delete sets DeletionTimestamp but keeps the object while the
			// stuck finalizer is present (mimics rv-controller finalizer).
			Expect(cl.Delete(ctx, rva)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(150 * time.Millisecond)
				cur := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, cur)).To(Succeed())
				cur.Finalizers = nil
				Expect(cl.Update(ctx, cur)).To(Succeed())
			}()

			waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := WaitForRVADeleted(waitCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			got := &v1alpha1.ReplicatedVolumeAttachment{}
			getErr := cl.Get(ctx, client.ObjectKey{Name: rvaName}, got)
			Expect(getErr).To(HaveOccurred())
		})
	})

	Context("when context deadline expires while RVA is stuck", func() {
		It("should return a wrapped DeadlineExceeded error carrying diagnostics", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"
			rvaName := BuildRVAName(volumeName, nodeName)

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rvaName,
					Finalizers: []string{stuckFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: volumeName,
					NodeName:             nodeName,
				},
			}
			Expect(cl.Create(ctx, rva)).To(Succeed())
			Expect(cl.Delete(ctx, rva)).To(Succeed())

			timeoutCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()

			err := WaitForRVADeleted(timeoutCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring(stuckFinalizer))
		})
	})

	Context("when context is cancelled", func() {
		It("should return a wrapped Canceled error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"
			rvaName := BuildRVAName(volumeName, nodeName)

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rvaName,
					Finalizers: []string{stuckFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: volumeName,
					NodeName:             nodeName,
				},
			}
			Expect(cl.Create(ctx, rva)).To(Succeed())
			Expect(cl.Delete(ctx, rva)).To(Succeed())

			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()

			err := WaitForRVADeleted(cancelledCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		})
	})
})

var _ = Describe("EnsureRVA with stale deleting RVA", func() {
	var (
		cl      client.Client
		log     logger.Logger
		traceID string
	)

	const stuckFinalizer = "test.deckhouse.io/stuck"

	BeforeEach(func() {
		cl = newFakeClient()
		log = logger.WrapLorg(GinkgoLogr)
		traceID = "test-trace-id"
	})

	Context("when stale RVA gets deleted before the deadline", func() {
		It("should wait for deletion and then create a fresh RVA", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"
			rvaName := BuildRVAName(volumeName, nodeName)

			stale := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rvaName,
					Finalizers: []string{stuckFinalizer, SDSReplicatedVolumeCSIFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: volumeName,
					NodeName:             nodeName,
				},
			}
			Expect(cl.Create(ctx, stale)).To(Succeed())
			Expect(cl.Delete(ctx, stale)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(150 * time.Millisecond)
				cur := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, cur)).To(Succeed())
				cur.Finalizers = nil
				Expect(cl.Update(ctx, cur)).To(Succeed())
			}()

			ensureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			got, err := EnsureRVA(ensureCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(rvaName))

			fresh := &v1alpha1.ReplicatedVolumeAttachment{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, fresh)).To(Succeed())
			Expect(fresh.DeletionTimestamp).To(BeNil())
			Expect(fresh.Finalizers).To(ContainElement(SDSReplicatedVolumeCSIFinalizer))
		})
	})

	Context("when stale RVA is stuck past the deadline", func() {
		It("should return a DeadlineExceeded error instead of creating a new RVA", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"
			rvaName := BuildRVAName(volumeName, nodeName)

			stale := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:       rvaName,
					Finalizers: []string{stuckFinalizer, SDSReplicatedVolumeCSIFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: volumeName,
					NodeName:             nodeName,
				},
			}
			Expect(cl.Create(ctx, stale)).To(Succeed())
			Expect(cl.Delete(ctx, stale)).To(Succeed())

			ensureCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()

			_, err := EnsureRVA(ensureCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())

			// Stale object is still present with its DeletionTimestamp;
			// no second RVA has been created in its place.
			got := &v1alpha1.ReplicatedVolumeAttachment{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: rvaName}, got)).To(Succeed())
			Expect(got.DeletionTimestamp).NotTo(BeNil())
		})
	})
})

// Helper functions

func newFakeClient() client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha1.AddToScheme(s)

	builder := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{})
	return builder.Build()
}

