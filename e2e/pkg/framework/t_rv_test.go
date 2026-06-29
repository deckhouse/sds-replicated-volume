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

package framework

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

func makeRVR(name, phase string) *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	rvr.Name = name
	rvr.Status.Phase = v1alpha1.ReplicatedVolumeReplicaPhase(phase)
	return rvr
}

func newTestRVForTest(name string) *TestRV {
	trv := &TestRV{
		TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, name, tk.Lifecycle[*v1alpha1.ReplicatedVolume]{}),
	}
	trv.rvrs = tk.NewTrackedGroup(nil, nil, schema.GroupVersionKind{},
		func(rvrName string) *TestRVR {
			return &TestRVR{
				TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, rvrName, tk.Lifecycle[*v1alpha1.ReplicatedVolumeReplica]{}),
				buildRVName:   name,
			}
		},
	)
	trv.rvas = tk.NewTrackedGroup(nil, nil, schema.GroupVersionKind{},
		func(rvaName string) *TestRVA {
			return &TestRVA{
				TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, rvaName, tk.Lifecycle[*v1alpha1.ReplicatedVolumeAttachment]{}),
			}
		},
	)
	return trv
}

var _ = Describe("TestRV builder", func() {
	It("Size sets the field", func() {
		trv := newTestRVForTest("test-rv").Size("200Mi")
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.Size.Cmp(resource.MustParse("200Mi"))).To(Equal(0))
	})

	It("unset Size defaults to 1Mi", func() {
		trv := newTestRVForTest("test-rv")
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.Size.String()).To(Equal("1Mi"))
	})

	It("MaxAttachments sets the field", func() {
		trv := newTestRVForTest("test-rv").MaxAttachments(3)
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.MaxAttachments).To(Equal(ptr.To(byte(3))))
	})

	It("unset MaxAttachments is nil (CRD default applies on server)", func() {
		trv := newTestRVForTest("test-rv")
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.MaxAttachments).To(BeNil())
	})

	It("FTT and GMDR store values", func() {
		trv := newTestRVForTest("test-rv").FTT(1).GMDR(2)
		Expect(*trv.buildFTT).To(Equal(byte(1)))
		Expect(*trv.buildGMDR).To(Equal(byte(2)))
	})

	It("unset FTT/GMDR are nil", func() {
		trv := newTestRVForTest("test-rv")
		Expect(trv.buildFTT).To(BeNil())
		Expect(trv.buildGMDR).To(BeNil())
	})

	It("RSCName sets replicatedStorageClassName", func() {
		trv := newTestRVForTest("test-rv").RSCName("my-rsc")
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.ReplicatedStorageClassName).To(Equal("my-rsc"))
		Expect(rv.Spec.ConfigurationMode).To(Equal(v1alpha1.ReplicatedVolumeConfigurationModeAuto))
	})

	It("ManualConfig sets manual mode", func() {
		cfg := v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "my-pool",
			Topology:                  v1alpha1.TopologyIgnored,
		}
		trv := newTestRVForTest("test-rv").ManualConfig(cfg)
		rv := trv.buildObject(context.Background())
		Expect(rv.Spec.ConfigurationMode).To(Equal(v1alpha1.ReplicatedVolumeConfigurationModeManual))
		Expect(rv.Spec.ManualConfiguration).NotTo(BeNil())
		Expect(rv.Spec.ManualConfiguration.ReplicatedStoragePoolName).To(Equal("my-pool"))
	})

	It("fluent chain returns same instance", func() {
		trv := newTestRVForTest("test-rv")
		same := trv.FTT(1).GMDR(0).Size("100Mi").MaxAttachments(1)
		Expect(same).To(BeIdenticalTo(trv))
	})

	It("name is set correctly", func() {
		trv := newTestRVForTest("my-volume")
		Expect(trv.Name()).To(Equal("my-volume"))
		rv := trv.buildObject(context.Background())
		Expect(rv.Name).To(Equal("my-volume"))
	})
})

var _ = Describe("TestRV child access", func() {
	It("TestRVR returns child by ID", func() {
		trv := newTestRVForTest("test-rv")
		trvr := trv.TestRVR(0)
		Expect(trvr).NotTo(BeNil())
		Expect(trvr.Name()).To(Equal("test-rv-0"))
	})

	It("TestRVR returns same instance for same ID", func() {
		trv := newTestRVForTest("test-rv")
		trvr1 := trv.TestRVR(0)
		trvr2 := trv.TestRVR(0)
		Expect(trvr1.TrackedObject).To(BeIdenticalTo(trvr2.TrackedObject))
	})

	It("RVRCount tracks injected events", func() {
		trv := newTestRVForTest("test-rv")
		Expect(trv.RVRCount()).To(Equal(0))

		trv.rvrs.InjectEvent("test-rv-0", watch.Added, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhasePending)))
		Expect(trv.RVRCount()).To(Equal(1))

		trv.rvrs.InjectEvent("test-rv-1", watch.Added, makeRVR("test-rv-1", string(v1alpha1.ReplicatedVolumeReplicaPhasePending)))
		Expect(trv.RVRCount()).To(Equal(2))
	})

	It("injectRVREvent routes to correct child", func() {
		trv := newTestRVForTest("test-rv")
		trv.rvrs.InjectEvent("test-rv-0", watch.Added, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

		trvr := trv.TestRVR(0)
		Expect(trvr.Object()).To(tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
	})

	It("OnEachRVR returns GroupHandle", func() {
		trv := newTestRVForTest("test-rv")
		h := trv.OnEachRVR()
		Expect(h).NotTo(BeNil())
	})
})

var _ = Describe("TestRV safety invariants", func() {
	It("ActivateSafetyInvariants registers checks on RV", func() {
		trv := newTestRVForTest("test-rv")
		trv.ActivateSafetyInvariants()
	})

	It("ActivateSafetyInvariants registers checks on RVR children", func() {
		trv := newTestRVForTest("test-rv")
		trv.ActivateSafetyInvariants()

		trv.rvrs.InjectEvent("test-rv-0", watch.Added, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhasePending)))
	})

	It("WithoutSafetyInvariants disables checks during fn", func() {
		trv := newTestRVForTest("test-rv")
		trv.ActivateSafetyInvariants()

		trv.rvrs.InjectEvent("test-rv-0", watch.Added, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhasePending)))

		trv.WithoutSafetyInvariants(func() {
			trv.rvrs.InjectEvent("test-rv-0", watch.Modified, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical)))
		})

		trvr := trv.TestRVR(0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(func() { trvr.Await(ctx, BeNil()) }).NotTo(Panic())
	})

	It("safety checks fire after WithoutSafetyInvariants scope", func() {
		trv := newTestRVForTest("test-rv")
		trv.ActivateSafetyInvariants()

		trv.rvrs.InjectEvent("test-rv-0", watch.Added, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhasePending)))

		trv.WithoutSafetyInvariants(func() {
			trv.rvrs.InjectEvent("test-rv-0", watch.Modified, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical)))
		})

		// After scope — checks are re-enabled. New violation should fire.
		trv.rvrs.InjectEvent("test-rv-0", watch.Modified, makeRVR("test-rv-0", string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical)))

		trvr := trv.TestRVR(0)
		err := InterceptGomegaFailure(func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			trvr.Await(ctx, BeNil())
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("check violated"))
	})

	It("QuorumCorrect fires on RV itself", func() {
		trv := newTestRVForTest("test-rv")
		trv.ActivateSafetyInvariants()

		rv := &v1alpha1.ReplicatedVolume{}
		rv.Name = "test-rv"
		rv.Status.Datamesh.Quorum = 99
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "test-rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1",
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}},
		}
		trv.InjectEvent(watch.Modified, rv)

		err := InterceptGomegaFailure(func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			trv.Await(ctx, BeNil())
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("quorum"))
	})

	It("WithoutSafetyInvariants panics if not activated", func() {
		trv := newTestRVForTest("test-rv")
		Expect(func() {
			trv.WithoutSafetyInvariants(func() {})
		}).To(PanicWith("WithoutSafetyInvariants called before ActivateSafetyInvariants"))
	})
})

var _ = Describe("TestRV Create stub", func() {
	It("Create is no-op with nil client", func() {
		trv := newTestRVForTest("test-rv").Size("100Mi")
		trv.Create(context.Background())
		Expect(trv.Name()).To(Equal("test-rv"))
	})
})

var _ = Describe("TestRV Get stub", func() {
	It("GetExpect is no-op with nil client", func() {
		trv := newTestRVForTest("test-rv")
		trv.GetExpect(context.Background(), Succeed())
		Expect(trv.Name()).To(Equal("test-rv"))
	})

	It("Get is no-op with nil client", func() {
		trv := newTestRVForTest("test-rv")
		trv.Get(context.Background())
		Expect(trv.Name()).To(Equal("test-rv"))
	})
})

var _ = Describe("TestRV injectRVAEvent", func() {
	It("routes to rvas group", func() {
		trv := newTestRVForTest("test-rv")
		rva := &v1alpha1.ReplicatedVolumeAttachment{}
		rva.Name = "test-rv-node1"
		rva.Spec.ReplicatedVolumeName = "test-rv"
		rva.Spec.NodeName = "node1"
		trv.rvas.InjectEvent("test-rv-node1", watch.Added, rva)
		Expect(trv.rvas.Count()).To(Equal(1))
	})
})

var _ = Describe("TestRV pool type and topology", func() {
	It("stores pool type", func() {
		trv := newTestRVForTest("test-rv").WithPoolType(v1alpha1.ReplicatedStoragePoolTypeLVM)
		Expect(*trv.buildPoolType).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVM))
	})

	It("stores topology", func() {
		trv := newTestRVForTest("test-rv").Topology(v1alpha1.TopologyTransZonal)
		Expect(*trv.buildTopology).To(Equal(v1alpha1.TopologyTransZonal))
	})

	It("stores volume access", func() {
		trv := newTestRVForTest("test-rv").VolumeAccess(v1alpha1.VolumeAccessLocal)
		Expect(*trv.buildAccess).To(Equal(v1alpha1.VolumeAccessLocal))
	})
})
