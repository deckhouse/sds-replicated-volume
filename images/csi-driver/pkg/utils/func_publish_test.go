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

	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2old"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

func TestPublishUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Publish Utils Suite")
}

var _ = Describe("AddPublishRequested", func() {
	var (
		ctx     context.Context
		cl      client.Client
		log     *logger.Logger
		traceID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		log, _ = logger.NewLogger(logger.InfoLevel)
		traceID = "test-trace-id"
	})

	Context("when adding node to empty publishRequested", func() {
		It("should successfully add the node", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddPublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeName))
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(1))
		})
	})

	Context("when adding second node", func() {
		It("should successfully add the second node", func() {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddPublishRequested(ctx, cl, log, traceID, volumeName, nodeName2)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeName1))
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeName2))
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(2))
		})
	})

	Context("when node already exists", func() {
		It("should return nil without error", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddPublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(1))
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeName))
		})
	})

	Context("when maximum nodes already present", func() {
		It("should return an error", func() {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"
			nodeName3 := "node-3"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1, nodeName2})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := AddPublishRequested(ctx, cl, log, traceID, volumeName, nodeName3)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("maximum of 2 nodes already present"))

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(2))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return an error", func() {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := AddPublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("get ReplicatedVolume"))
		})
	})
})

var _ = Describe("RemovePublishRequested", func() {
	var (
		ctx     context.Context
		cl      client.Client
		log     *logger.Logger
		traceID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		log, _ = logger.NewLogger(logger.InfoLevel)
		traceID = "test-trace-id"
	})

	Context("when removing existing node", func() {
		It("should successfully remove the node", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemovePublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).NotTo(ContainElement(nodeName))
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(0))
		})
	})

	Context("when removing one node from two", func() {
		It("should successfully remove one node and keep the other", func() {
			volumeName := "test-volume"
			nodeName1 := "node-1"
			nodeName2 := "node-2"

			rv := createTestReplicatedVolume(volumeName, []string{nodeName1, nodeName2})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemovePublishRequested(ctx, cl, log, traceID, volumeName, nodeName1)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).NotTo(ContainElement(nodeName1))
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeName2))
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(1))
		})
	})

	Context("when node does not exist", func() {
		It("should return nil without error", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := RemovePublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())

			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
			Expect(len(updatedRV.Spec.PublishRequested)).To(Equal(0))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return nil (considered success)", func() {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := RemovePublishRequested(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("WaitForPublishProvided", func() {
	var (
		ctx     context.Context
		cl      client.Client
		log     *logger.Logger
		traceID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		log, _ = logger.NewLogger(logger.InfoLevel)
		traceID = "test-trace-id"
	})

	Context("when node already in publishProvided", func() {
		It("should return immediately", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{nodeName},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForPublishProvided(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node appears in publishProvided", func() {
		It("should wait and return successfully", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.PublishProvided = []string{nodeName}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := WaitForPublishProvided(timeoutCtx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return an error", func() {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := WaitForPublishProvided(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ReplicatedVolume"))
		})
	})

	Context("when context is cancelled", func() {
		It("should return context error", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()

			err := WaitForPublishProvided(cancelledCtx, cl, log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(context.Canceled))
		})
	})
})

var _ = Describe("WaitForPublishRemoved", func() {
	var (
		ctx     context.Context
		cl      client.Client
		log     *logger.Logger
		traceID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		log, _ = logger.NewLogger(logger.InfoLevel)
		traceID = "test-trace-id"
	})

	Context("when node already not in publishProvided", func() {
		It("should return immediately", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForPublishRemoved(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when node is removed from publishProvided", func() {
		It("should wait and return successfully", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{nodeName},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background after a short delay
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeName}, updatedRV)).To(Succeed())
				updatedRV.Status.PublishProvided = []string{}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := WaitForPublishRemoved(timeoutCtx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return nil (considered success)", func() {
			volumeName := "non-existent-volume"
			nodeName := "node-1"

			err := WaitForPublishRemoved(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when status is nil", func() {
		It("should return nil (considered success)", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = nil
			Expect(cl.Create(ctx, rv)).To(Succeed())

			err := WaitForPublishRemoved(ctx, cl, log, traceID, volumeName, nodeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when context is cancelled", func() {
		It("should return context error", func() {
			volumeName := "test-volume"
			nodeName := "node-1"

			rv := createTestReplicatedVolume(volumeName, []string{})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{nodeName},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()

			err := WaitForPublishRemoved(cancelledCtx, cl, log, traceID, volumeName, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(context.Canceled))
		})
	})
})

// Helper functions

func newFakeClient() client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha2.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s)
	return builder.Build()
}

func createTestReplicatedVolume(name string, publishRequested []string) *v1alpha2.ReplicatedVolume {
	return &v1alpha2.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha2.ReplicatedVolumeSpec{
			Size:             resource.MustParse("1Gi"),
			Replicas:         3,
			SharedSecret:     "test-secret",
			Topology:         "Zonal",
			VolumeAccess:     "PreferablyLocal",
			PublishRequested: publishRequested,
			LVM: v1alpha2.LVMSpec{
				Type: "Thick",
				LVMVolumeGroups: []v1alpha2.LVGRef{
					{
						Name: "test-vg",
					},
				},
			},
		},
		Status: &v1alpha2.ReplicatedVolumeStatus{
			PublishProvided: []string{},
		},
	}
}
