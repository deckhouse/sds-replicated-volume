/*
Copyright 2025 Flant JSC

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

package utils

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
	RunSpecs(t, "Attach Utils Suite")
}

var _ = Describe("AddAttachTo", func() {
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

	Context("when adding node to empty spec.attachTo", func() {
		It("should successfully add the node", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.AttachTo).To(ContainElement(nodeName))
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(1))
		})
	})

	Context("when adding second node", func() {
		It("should successfully add the second node", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddAttachTo(ctx, cl, &log, traceID, volumeName, nodeName2)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.AttachTo).To(ContainElement(nodeName1))
			Expect(updatedRV.Spec.AttachTo).To(ContainElement(nodeName2))
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(2))
		})
	})

	Context("when node already exists", func() {
		It("should return nil without error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(1))
			Expect(updatedRV.Spec.AttachTo).To(ContainElement(nodeName))
		})
	})

	Context("when maximum nodes already present", func() {
		It("should return an error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"
			nodeName3 := "node-3"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1, nodeName2})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddAttachTo(ctx, cl, &log, traceID, volumeName, nodeName3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("maximum of 2 nodes already present"))

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(2))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return an error", func(ctx SpecContext) {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := AddAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("get ReplicatedVolume"))
		})
	})
})

var _ = Describe("RemoveAttachTo", func() {
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

	Context("when removing existing node", func() {
		It("should successfully remove the node", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemoveAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.AttachTo).NotTo(ContainElement(nodeName))
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(0))
		})
	})

	Context("when removing one node from two", func() {
		It("should successfully remove one node and keep the other", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1, nodeName2})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemoveAttachTo(ctx, cl, &log, traceID, volumeName, nodeName1)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.AttachTo).NotTo(ContainElement(nodeName1))
			Expect(updatedRV.Spec.AttachTo).To(ContainElement(nodeName2))
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(1))
		})
	})

	Context("when node does not exist", func() {
		It("should return nil without error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemoveAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.AttachTo)).To(Equal(0))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return nil (considered success)", func(ctx SpecContext) {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := RemoveAttachTo(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
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

	Context("when node already in status.attachedTo", func() {
		It("should return immediately", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{nodeName},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToProvided(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node appears in status.attachedTo", func() {
		It("should wait and return successfully", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.AttachedTo = []string{nodeName}
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

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{},
			}
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

	Context("when node already not in status.attachedTo", func() {
		It("should return immediately", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToRemoved(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node is removed from status.attachedTo", func() {
		It("should wait and return successfully", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{nodeName},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.AttachedTo = []string{}
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

	Context("when status is nil", func() {
		It("should return nil (considered success)", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = nil
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForAttachedToRemoved(ctx, cl, &log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when context is cancelled", func() {
		It("should return context error", func(ctx SpecContext) {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{
				AttachedTo: []string{nodeName},
			}
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

	builder := fake.NewClientBuilder().WithScheme(s)
	return builder.Build()
}

func createTestReplicatedVolume(name string, attachTo []string) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       resource.MustParse("1Gi"),
			AttachTo:                   attachTo,
			ReplicatedStorageClassName: "rsc",
		},
		Status: &v1alpha1.ReplicatedVolumeStatus{
			AttachedTo: []string{},
		},
	}
}
