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

package driver

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/csi/internal"
	"github.com/deckhouse/sds-replicated-volume/images/csi/pkg/logger"
)

func TestControllerPublish(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Publish Suite")
}

var _ = Describe("ControllerPublishVolume", func() {
	var (
		ctx    context.Context
		cl     client.Client
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClientForDriver()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	Context("when publishing volume successfully", func() {
		It("should return success with correct PublishContext", func() {
			volumeID := "test-volume"
			nodeID := "node-1"

			rv := createTestReplicatedVolumeForDriver(volumeID, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background to simulate controller updating publishProvided
			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
				updatedRV.Status.PublishProvided = []string{nodeID}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.ControllerPublishVolumeRequest{
				VolumeId: volumeID,
				NodeId:   nodeID,
			}

			response, err := driver.ControllerPublishVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.PublishContext).To(HaveKey(internal.ReplicatedVolumeNameKey))
			Expect(response.PublishContext[internal.ReplicatedVolumeNameKey]).To(Equal(volumeID))

			// Verify that node was added to publishRequested
			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).To(ContainElement(nodeID))
		})
	})

	Context("when VolumeId is empty", func() {
		It("should return InvalidArgument error", func() {
			request := &csi.ControllerPublishVolumeRequest{
				VolumeId: "",
				NodeId:   "node-1",
			}

			response, err := driver.ControllerPublishVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Context("when NodeId is empty", func() {
		It("should return InvalidArgument error", func() {
			request := &csi.ControllerPublishVolumeRequest{
				VolumeId: "test-volume",
				NodeId:   "",
			}

			response, err := driver.ControllerPublishVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return Internal error", func() {
			request := &csi.ControllerPublishVolumeRequest{
				VolumeId: "non-existent-volume",
				NodeId:   "node-1",
			}

			response, err := driver.ControllerPublishVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})
	})
})

var _ = Describe("ControllerUnpublishVolume", func() {
	var (
		ctx    context.Context
		cl     client.Client
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClientForDriver()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	Context("when unpublishing volume successfully", func() {
		It("should return success", func() {
			volumeID := "test-volume"
			nodeID := "node-1"

			rv := createTestReplicatedVolumeForDriver(volumeID, []string{nodeID})
			rv.Status = &v1alpha2.ReplicatedVolumeStatus{
				PublishProvided: []string{nodeID},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background to simulate controller removing from publishProvided
			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
				updatedRV.Status.PublishProvided = []string{}
				// Use Update instead of Status().Update for fake client
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.ControllerUnpublishVolumeRequest{
				VolumeId: volumeID,
				NodeId:   nodeID,
			}

			response, err := driver.ControllerUnpublishVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			// Verify that node was removed from publishRequested
			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.PublishRequested).NotTo(ContainElement(nodeID))
		})
	})

	Context("when VolumeId is empty", func() {
		It("should return InvalidArgument error", func() {
			request := &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "",
				NodeId:   "node-1",
			}

			response, err := driver.ControllerUnpublishVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Context("when NodeId is empty", func() {
		It("should return InvalidArgument error", func() {
			request := &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "test-volume",
				NodeId:   "",
			}

			response, err := driver.ControllerUnpublishVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Context("when ReplicatedVolume does not exist", func() {
		It("should return success (considered as already unpublished)", func() {
			request := &csi.ControllerUnpublishVolumeRequest{
				VolumeId: "non-existent-volume",
				NodeId:   "node-1",
			}

			response, err := driver.ControllerUnpublishVolume(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
		})
	})
})

// Helper functions for driver tests

func newFakeClientForDriver() client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha2.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s)
	return builder.Build()
}

func createTestReplicatedVolumeForDriver(name string, publishRequested []string) *v1alpha2.ReplicatedVolume {
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

