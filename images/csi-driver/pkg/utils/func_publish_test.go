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
	"k8s.io/apimachinery/pkg/api/resource"
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

var _ = Describe("WaitForAttachedToProvided", func() {
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

	Context("when node already in status.actuallyAttachedTo", func() {
		It("should return immediately", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			rv.Status.ActuallyAttachedTo = []string{nodeName}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToProvided(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node appears in status.actuallyAttachedTo", func() {
		It("should wait and return successfully", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.ActuallyAttachedTo = []string{nodeName}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := WaitForAttachedToProvided(timeoutCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return an error", func(ctx SpecContext) {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := WaitForAttachedToProvided(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ReplicatedVolume"))
		})
	})

	Context("when context is cancelled", func() {
		It("should return context error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			Expect(cl.Create(ctx, rv)).To(Succeed())

			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()

			err := WaitForAttachedToProvided(cancelledCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(context.Canceled))
		})
	})
})

var _ = Describe("WaitForAttachedToRemoved", func() {
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

	Context("when node already not in status.actuallyAttachedTo", func() {
		It("should return immediately", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToRemoved(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node is removed from status.actuallyAttachedTo", func() {
		It("should wait and return successfully", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			rv.Status.ActuallyAttachedTo = []string{nodeName}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.ActuallyAttachedTo = []string{}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := WaitForAttachedToRemoved(timeoutCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return nil (considered success)", func(ctx SpecContext) {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := WaitForAttachedToRemoved(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when status is empty", func() {
		It("should return nil (considered success)", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			rv.Status = v1alpha1.ReplicatedVolumeStatus{}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToRemoved(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when context is cancelled", func() {
		It("should return context error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName)
			rv.Status.ActuallyAttachedTo = []string{nodeName}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()

			err := WaitForAttachedToRemoved(cancelledCtx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(context.Canceled))
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

func createTestReplicatedVolume(name string) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       resource.MustParse("1Gi"),
			ReplicatedStorageClassName: "rsc",
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			ActuallyAttachedTo: []string{},
		},
	}
}
