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
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/internal"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

var _ = Describe("CreateVolume", func() {
	var (
		ctx    context.Context
		cl     client.Client
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClientForController()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	Context("when creating volume successfully", func() {
		It("should create ReplicatedVolume and return success", func() {
			// Create test ReplicatedStoragePool
			rsp := createTestReplicatedStoragePool("test-pool", []string{"test-vg"})
			Expect(cl.Create(ctx, rsp)).To(Succeed())

			// Create test LVMVolumeGroup
			lvg := createTestLVMVolumeGroup("test-vg", "node-1")
			Expect(cl.Create(ctx, lvg)).To(Succeed())

			// Update status in background to simulate controller making volume ready
			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume"}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			// Use context with timeout to prevent hanging
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.CreateVolumeRequest{
				Name: "test-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824, // 1Gi
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{
					internal.StoragePoolKey: "test-pool",
				},
			}

			response, err := driver.CreateVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.Volume).NotTo(BeNil())
			Expect(response.Volume.VolumeId).To(Equal("test-volume"))
			Expect(response.Volume.CapacityBytes).To(Equal(int64(1073741824)))
			Expect(response.Volume.VolumeContext).To(HaveKey(internal.ReplicatedVolumeNameKey))
			Expect(response.Volume.VolumeContext[internal.ReplicatedVolumeNameKey]).To(Equal("test-volume"))

			// Verify that ReplicatedVolume was created
			rv := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume"}, rv)).To(Succeed())
			Expect(rv.Spec.Size.Value()).To(Equal(int64(1073741824)))
			Expect(rv.Spec.Replicas).To(Equal(byte(3))) // default
			Expect(rv.Spec.Topology).To(Equal("Zonal")) // default
		})

		It("should parse custom parameters correctly", func() {
			// Create test ReplicatedStoragePool with thin pool
			rsp := &srv.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: srv.ReplicatedStoragePoolSpec{
					Type: "LVMThin",
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{
						{
							Name:         "test-vg",
							ThinPoolName: "test-pool",
						},
					},
				},
			}
			Expect(cl.Create(ctx, rsp)).To(Succeed())

			lvg := createTestLVMVolumeGroup("test-vg", "node-1")
			Expect(cl.Create(ctx, lvg)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume"}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.CreateVolumeRequest{
				Name: "test-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 2147483648, // 2Gi
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{
					internal.StoragePoolKey: "test-pool",
					ReplicasKey:             "5",
					TopologyKey:             "TransZonal",
					VolumeAccessKey:         "Local",
					ZonesKey:                "- zone-1\n- zone-2\n- zone-3",
				},
			}

			response, err := driver.CreateVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			// Verify ReplicatedVolume spec
			rv := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume"}, rv)).To(Succeed())
			Expect(rv.Spec.Size.Value()).To(Equal(int64(2147483648)))
			Expect(rv.Spec.Replicas).To(Equal(byte(5)))
			Expect(rv.Spec.Topology).To(Equal("TransZonal"))
			Expect(rv.Spec.VolumeAccess).To(Equal("Local"))
			Expect(rv.Spec.SharedSecret).NotTo(BeEmpty()) // sharedSecret is auto-generated UUID
			Expect(rv.Spec.Zones).To(Equal([]string{"zone-1", "zone-2", "zone-3"}))
			Expect(rv.Spec.LVM.Type).To(Equal(internal.LVMTypeThin))
			Expect(rv.Spec.LVM.LVMVolumeGroups).To(HaveLen(1))
			Expect(rv.Spec.LVM.LVMVolumeGroups[0].ThinPoolName).To(Equal("test-pool"))
		})

		It("should parse zones in YAML format correctly", func() {
			rsp := &srv.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: srv.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{
						{
							Name: "test-vg",
						},
					},
				},
			}
			Expect(cl.Create(ctx, rsp)).To(Succeed())

			lvg := createTestLVMVolumeGroup("test-vg", "node-1")
			Expect(cl.Create(ctx, lvg)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-yaml"}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.CreateVolumeRequest{
				Name: "test-volume-yaml",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{
					internal.StoragePoolKey: "test-pool",
					TopologyKey:             "TransZonal",
					ZonesKey:                "- zone-a\n- zone-b\n- zone-c",
				},
			}

			response, err := driver.CreateVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			rv := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-yaml"}, rv)).To(Succeed())
			Expect(rv.Spec.Zones).To(Equal([]string{"zone-a", "zone-b", "zone-c"}))
		})

		It("should parse single zone in YAML format correctly", func() {
			rsp := &srv.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: srv.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{
						{
							Name: "test-vg",
						},
					},
				},
			}
			Expect(cl.Create(ctx, rsp)).To(Succeed())

			lvg := createTestLVMVolumeGroup("test-vg", "node-1")
			Expect(cl.Create(ctx, lvg)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-single"}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.CreateVolumeRequest{
				Name: "test-volume-single",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{
					internal.StoragePoolKey: "test-pool",
					TopologyKey:             "TransZonal",
					ZonesKey:                "- single-zone",
				},
			}

			response, err := driver.CreateVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			rv := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-single"}, rv)).To(Succeed())
			Expect(rv.Spec.Zones).To(Equal([]string{"single-zone"}))
		})

		It("should handle empty zones parameter", func() {
			rsp := &srv.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pool",
				},
				Spec: srv.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{
						{
							Name: "test-vg",
						},
					},
				},
			}
			Expect(cl.Create(ctx, rsp)).To(Succeed())

			lvg := createTestLVMVolumeGroup("test-vg", "node-1")
			Expect(cl.Create(ctx, lvg)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-empty"}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.CreateVolumeRequest{
				Name: "test-volume-empty",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{
					internal.StoragePoolKey: "test-pool",
					TopologyKey:             "Zonal",
				},
			}

			response, err := driver.CreateVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			rv := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-volume-empty"}, rv)).To(Succeed())
			Expect(rv.Spec.Zones).To(BeEmpty())
		})
	})

	Context("when validation fails", func() {
		It("should return error when volume name is empty", func() {
			request := &csi.CreateVolumeRequest{
				Name: "",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{},
			}

			response, err := driver.CreateVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("should return error when volume capabilities are empty", func() {
			request := &csi.CreateVolumeRequest{
				Name: "test-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: nil,
				Parameters:         map[string]string{},
			}

			response, err := driver.CreateVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("should return error when StoragePool is empty", func() {
			request := &csi.CreateVolumeRequest{
				Name: "test-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824,
				},
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
				Parameters: map[string]string{},
			}

			response, err := driver.CreateVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})
})

var _ = Describe("DeleteVolume", func() {
	var (
		ctx    context.Context
		cl     client.Client
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClientForController()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	Context("when deleting volume successfully", func() {
		It("should delete ReplicatedVolume and return success", func() {
			volumeID := "test-volume"
			rv := createTestReplicatedVolumeForDriver(volumeID, []string{})
			Expect(cl.Create(ctx, rv)).To(Succeed())

			request := &csi.DeleteVolumeRequest{
				VolumeId: volumeID,
			}

			response, err := driver.DeleteVolume(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())

			// Verify that ReplicatedVolume was deleted
			rvAfterDelete := &v1alpha2.ReplicatedVolume{}
			err = cl.Get(ctx, client.ObjectKey{Name: volumeID}, rvAfterDelete)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(Succeed())
		})

		It("should return success when volume does not exist", func() {
			request := &csi.DeleteVolumeRequest{
				VolumeId: "non-existent-volume",
			}

			response, err := driver.DeleteVolume(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
		})
	})

	Context("when validation fails", func() {
		It("should return error when VolumeId is empty", func() {
			request := &csi.DeleteVolumeRequest{
				VolumeId: "",
			}

			response, err := driver.DeleteVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})
})

var _ = Describe("ControllerExpandVolume", func() {
	var (
		ctx    context.Context
		cl     client.Client
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClientForController()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	Context("when expanding volume successfully", func() {
		It("should expand ReplicatedVolume and return success", func() {
			volumeID := "test-volume"
			rv := createTestReplicatedVolumeForDriver(volumeID, []string{})
			rv.Spec.Size = resource.MustParse("1Gi")
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Update status in background to simulate controller making volume ready after resize
			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.ControllerExpandVolumeRequest{
				VolumeId: volumeID,
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 2147483648, // 2Gi
				},
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "ext4",
						},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			}

			response, err := driver.ControllerExpandVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.CapacityBytes).To(Equal(int64(2147483648)))
			Expect(response.NodeExpansionRequired).To(BeTrue())

			// Verify that ReplicatedVolume size was updated
			updatedRV := &v1alpha2.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
			Expect(updatedRV.Spec.Size.Value()).To(Equal(int64(2147483648)))
		})

		It("should return success without resize when requested size is less than current size", func() {
			volumeID := "test-volume"
			rv := createTestReplicatedVolumeForDriver(volumeID, []string{})
			rv.Spec.Size = resource.MustParse("2Gi")
			Expect(cl.Create(ctx, rv)).To(Succeed())

			request := &csi.ControllerExpandVolumeRequest{
				VolumeId: volumeID,
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 1073741824, // 1Gi (less than current 2Gi)
				},
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			}

			response, err := driver.ControllerExpandVolume(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.CapacityBytes).To(Equal(int64(2147483648))) // Should return current size
			Expect(response.NodeExpansionRequired).To(BeTrue())
		})

		It("should set NodeExpansionRequired to false for block volumes", func() {
			volumeID := "test-volume"
			rv := createTestReplicatedVolumeForDriver(volumeID, []string{})
			rv.Spec.Size = resource.MustParse("1Gi")
			Expect(cl.Create(ctx, rv)).To(Succeed())

			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				updatedRV := &v1alpha2.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: volumeID}, updatedRV)).To(Succeed())
				updatedRV.Status = &v1alpha2.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha2.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				Expect(cl.Update(ctx, updatedRV)).To(Succeed())
			}()

			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			request := &csi.ControllerExpandVolumeRequest{
				VolumeId: volumeID,
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 2147483648, // 2Gi
				},
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			}

			response, err := driver.ControllerExpandVolume(timeoutCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response).NotTo(BeNil())
			Expect(response.NodeExpansionRequired).To(BeFalse())
		})
	})

	Context("when validation fails", func() {
		It("should return error when VolumeId is empty", func() {
			request := &csi.ControllerExpandVolumeRequest{
				VolumeId: "",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 2147483648,
				},
			}

			response, err := driver.ControllerExpandVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("should return error when ReplicatedVolume does not exist", func() {
			request := &csi.ControllerExpandVolumeRequest{
				VolumeId: "non-existent-volume",
				CapacityRange: &csi.CapacityRange{
					RequiredBytes: 2147483648,
				},
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			}

			response, err := driver.ControllerExpandVolume(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})
	})
})

var _ = Describe("ControllerGetCapabilities", func() {
	var (
		ctx    context.Context
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl := newFakeClientForController()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	It("should return correct capabilities", func() {
		request := &csi.ControllerGetCapabilitiesRequest{}

		response, err := driver.ControllerGetCapabilities(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response).NotTo(BeNil())
		Expect(response.Capabilities).NotTo(BeNil())
		Expect(len(response.Capabilities)).To(BeNumerically(">", 0))

		capabilityTypes := make(map[csi.ControllerServiceCapability_RPC_Type]bool)
		for _, cap := range response.Capabilities {
			Expect(cap.Type).NotTo(BeNil())
			Expect(cap.Type).To(BeAssignableToTypeOf(&csi.ControllerServiceCapability_Rpc{}))
			rpc := cap.Type.(*csi.ControllerServiceCapability_Rpc)
			capabilityTypes[rpc.Rpc.Type] = true
		}

		Expect(capabilityTypes[csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME]).To(BeTrue())
		Expect(capabilityTypes[csi.ControllerServiceCapability_RPC_CLONE_VOLUME]).To(BeTrue())
		Expect(capabilityTypes[csi.ControllerServiceCapability_RPC_GET_CAPACITY]).To(BeTrue())
		Expect(capabilityTypes[csi.ControllerServiceCapability_RPC_EXPAND_VOLUME]).To(BeTrue())
		Expect(capabilityTypes[csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME]).To(BeTrue())
	})
})

var _ = Describe("GetCapacity", func() {
	var (
		ctx    context.Context
		log    *logger.Logger
		driver *Driver
	)

	BeforeEach(func() {
		ctx = context.Background()
		cl := newFakeClientForController()
		log, _ = logger.NewLogger(logger.InfoLevel)
		nodeName := "test-node"
		driver, _ = NewDriver("unix:///tmp/test.sock", "test-driver", "127.0.0.1:12302", &nodeName, log, cl)
	})

	It("should return maximum capacity", func() {
		request := &csi.GetCapacityRequest{}

		response, err := driver.GetCapacity(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response).NotTo(BeNil())
		Expect(response.AvailableCapacity).To(Equal(int64(^uint64(0) >> 1))) // Max int64
		Expect(response.MaximumVolumeSize).To(BeNil())
		Expect(response.MinimumVolumeSize).To(BeNil())
	})
})

// Helper functions for controller tests

func newFakeClientForController() client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = srv.AddToScheme(s)
	_ = v1alpha2.AddToScheme(s)
	_ = snc.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s)
	return builder.Build()
}

func createTestReplicatedStoragePool(name string, lvgNames []string) *srv.ReplicatedStoragePool {
	lvgs := make([]srv.ReplicatedStoragePoolLVMVolumeGroups, 0, len(lvgNames))
	for _, lvgName := range lvgNames {
		lvgs = append(lvgs, srv.ReplicatedStoragePoolLVMVolumeGroups{
			Name:         lvgName,
			ThinPoolName: "",
		})
	}

	return &srv.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srv.ReplicatedStoragePoolSpec{
			Type:            "LVM",
			LVMVolumeGroups: lvgs,
		},
	}
}

func createTestLVMVolumeGroup(name, nodeName string) *snc.LVMVolumeGroup {
	return &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: snc.LVMVolumeGroupSpec{},
		Status: snc.LVMVolumeGroupStatus{
			Nodes: []snc.LVMVolumeGroupNode{
				{
					Name: nodeName,
				},
			},
		},
	}
}
