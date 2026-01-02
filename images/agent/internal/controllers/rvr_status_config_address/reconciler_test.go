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

package rvrstatusconfigaddress_test

import (
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrstatusconfigaddress "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/rvr_status_config_address"
)

var _ = Describe("Reconciler", func() {
	// Setup scheme
	s := scheme.Scheme
	Expect(metav1.AddMetaToScheme(s)).To(Succeed())
	Expect(corev1.AddToScheme(s)).To(Succeed())
	Expect(v1alpha1.AddToScheme(s)).To(Succeed())

	var (
		builder *fake.ClientBuilder
		cl      client.Client
		rec     *rvrstatusconfigaddress.Reconciler
		log     logr.Logger
		node    *corev1.Node
		drbdCfg testDRBDConfig
	)

	BeforeEach(func() {
		builder = fake.NewClientBuilder().
			WithScheme(s).
			WithStatusSubresource(
				&v1alpha1.ReplicatedVolumeReplica{},
				&v1alpha1.ReplicatedVolume{},
				&corev1.Node{},
			)

		cl = nil
		log = GinkgoLogr

		drbdCfg = testDRBDConfig{
			MinPort: 7000,
			MaxPort: 7999,
		}

		// Create test node with InternalIP
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalIP,
						Address: "192.168.1.10",
					},
				},
			},
		}
	})

	JustBeforeEach(func(ctx SpecContext) {
		// Create fake client with status subresource support
		cl = builder.Build()

		// Create reconciler using New method
		rec = rvrstatusconfigaddress.NewReconciler(cl, log, drbdCfg)

		// Create default objects if they are set
		if node != nil {
			Expect(cl.Create(ctx, node)).To(Succeed())
		}
	})

	It("should return no error when node does not exist (ignore not found)", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "non-existent-node"}})).
			ToNot(Requeue())
	})

	DescribeTableSubtree("when node has no",
		Entry("status", func() {
			node.Status = corev1.NodeStatus{}
		}),
		Entry("addresses", func() {
			node.Status.Addresses = []corev1.NodeAddress{}
		}),
		func(beforeEach func()) {
			BeforeEach(beforeEach)

			It("should return error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(node))).Error().
					To(MatchError(rvrstatusconfigaddress.ErrNodeMissingInternalIP))
			})
		})

	DescribeTableSubtree("when node has only",
		Entry("Hostname", corev1.NodeHostName),
		Entry("ExternalIP", corev1.NodeExternalIP),
		Entry("InternalDNS", corev1.NodeInternalDNS),
		Entry("ExternalDNS", corev1.NodeExternalDNS),
		func(addrType corev1.NodeAddressType) {
			DescribeTableSubtree("with address value",
				Entry("valid IPv4", "192.168.1.10"),
				Entry("valid IPv6", "2001:db8::1"),
				Entry("invalid format", "invalid-ip-address"),
				Entry("empty string", ""),
				Entry("hostname", "test-node"),
				Entry("DNS name", "test-node.example.com"),
				func(addrValue string) {
					BeforeEach(func() {
						node.Status.Addresses = []corev1.NodeAddress{{Type: addrType, Address: addrValue}}
					})

					It("should return error", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(node))).Error().To(Satisfy(func(err error) bool {
							return errors.Is(err, rvrstatusconfigaddress.ErrNodeMissingInternalIP)
						}))
					})
				})
		})

	It("should succeed without errors when there are no RVRs on the node", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())
	})

	When("RVs and RVRs created", func() {
		var (
			rvList           []v1alpha1.ReplicatedVolume
			rvrList          []v1alpha1.ReplicatedVolumeReplica
			otherNodeRVRList []v1alpha1.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			const count = 3

			rvList = make([]v1alpha1.ReplicatedVolume, count)
			rvrList = make([]v1alpha1.ReplicatedVolumeReplica, count)
			otherNodeRVRList = make([]v1alpha1.ReplicatedVolumeReplica, count)

			for i := range count {
				rvList[i] = v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("test-rv-%d", i+1)},
				}

				rvrList[i] = v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-%d-this-node", i+1)},
					Status: v1alpha1.ReplicatedVolumeReplicaStatus{
						Conditions: []metav1.Condition{},
						DRBD:       &v1alpha1.DRBD{Config: &v1alpha1.DRBDConfig{Address: &v1alpha1.Address{}}},
					},
				}
				rvrList[i].Spec.NodeName = node.Name
				Expect(rvrList[i].SetReplicatedVolume(&rvList[i], s)).To(Succeed())

				otherNodeRVRList[i] = v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-%d-other-node", i+1)},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "other-node"},
					Status: v1alpha1.ReplicatedVolumeReplicaStatus{
						Conditions: []metav1.Condition{},
						DRBD:       &v1alpha1.DRBD{Config: &v1alpha1.DRBDConfig{Address: &v1alpha1.Address{}}},
					},
				}
				Expect(otherNodeRVRList[i].SetReplicatedVolume(&rvList[i], s)).To(Succeed())
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			for i := range rvList {
				Expect(cl.Create(ctx, &rvList[i])).To(Succeed())
			}
			for i := range rvrList {
				Expect(cl.Create(ctx, &rvrList[i])).To(Succeed())
			}
			for i := range otherNodeRVRList {
				Expect(cl.Create(ctx, &otherNodeRVRList[i])).To(Succeed())
			}
		})

		It("should filter out RVRs on other nodes and not configure addresses", func(ctx SpecContext) {
			By("Saving previous versions")
			prev := make([]v1alpha1.ReplicatedVolumeReplica, len(otherNodeRVRList))
			for i := range otherNodeRVRList {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&otherNodeRVRList[i]), &prev[i])).To(Succeed())
			}

			By("Reconciling")
			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			By("Verifying all RVRs on other nodes are not modified")
			for i := range otherNodeRVRList {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&otherNodeRVRList[i]), &otherNodeRVRList[i])).To(Succeed())
			}
			Expect(otherNodeRVRList).To(Equal(prev))
		})

		When("single RVR", func() {
			var (
				rvr *v1alpha1.ReplicatedVolumeReplica
			)
			BeforeEach(func() {
				rvrList = rvrList[:1]
				rvr = &rvrList[0]
			})

			It("should configure address with first available port", func(ctx SpecContext) {
				By("using only first RVR for this test")
				Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

				By("verifying address was configured")
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
				Expect(rvr).To(SatisfyAll(
					HaveField("Status.DRBD.Config.Address.IPv4", Equal("192.168.1.10")),
					HaveField("Status.DRBD.Config.Address.Port", Equal(uint(7000))),
				))

				By("verifying condition was set")
				Expect(rvr).To(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", Equal(v1alpha1.RVRCondAddressConfiguredType)),
					HaveField("Status", Equal(metav1.ConditionTrue)),
					HaveField("Reason", Equal(v1alpha1.RVRCondAddressConfiguredReasonAddressConfigurationSucceeded)),
				))))
			})

			DescribeTableSubtree("should work with nil",
				Entry("Status", func() { rvr.Status = v1alpha1.ReplicatedVolumeReplicaStatus{} }),
				Entry("DRBD", func() { rvr.Status.DRBD = nil }),
				Entry("Config", func() { rvr.Status.DRBD.Config = nil }),
				Entry("Address", func() { rvr.Status.DRBD.Config.Address = nil }),
				func(beforeEach func()) {
					BeforeEach(beforeEach)

					It("should reconcile successfully and assign unique ports", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

						By("verifying all RVRs got unique ports in valid range")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())

						Expect(rvr).To(HaveField("Status.DRBD.Config.Address.Port", Satisfy(drbdCfg.IsPortValid)))
					})
				})

			When("RVR has different IP address", func() {
				BeforeEach(func() {
					rvr.Status = v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{Config: &v1alpha1.DRBDConfig{Address: &v1alpha1.Address{
							IPv4: "192.168.1.99", // different IP
							Port: 7500,
						}}},
					}
				})

				It("should update address but not port", func(ctx SpecContext) {
					originalPort := rvr.Status.DRBD.Config.Address.Port

					Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

					By("verifying all RVRs have address updated to node IP")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())

					Expect(rvr).To(HaveField("Status.DRBD.Config.Address.IPv4", Equal("192.168.1.10")))

					By("verifying port stayed the same for first RVR")
					Expect(rvr.Status.DRBD.Config.Address.Port).To(Equal(originalPort))
				})
			})
		})

		When("other node RVRs have ports", func() {
			BeforeEach(func() {
				// Set same ports on other node RVRs as will be assigned to this node RVRs
				for i := range otherNodeRVRList {
					otherNodeRVRList[i].Status.DRBD.Config.Address.IPv4 = "192.168.1.99"
					otherNodeRVRList[i].Status.DRBD.Config.Address.Port = uint(7000 + i) // Same ports as will be assigned
				}
			})

			It("should not interfere with RVRs on other nodes", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

				By("verifying RVRs on this node got unique ports (should skip used ports from other nodes)")
				for i := range rvrList {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[i]), &rvrList[i])).To(Succeed())
				}
				Expect(rvrList).To(SatisfyAll(
					HaveUniquePorts(),
					HaveEach(HaveField("Status.DRBD.Config.Address.Port", Satisfy(drbdCfg.IsPortValid)))))

				By("verifying RVRs on other nodes were not modified")
				for i := range otherNodeRVRList {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&otherNodeRVRList[i]), &otherNodeRVRList[i])).To(Succeed())
					Expect(otherNodeRVRList[i].Status.DRBD.Config.Address.Port).To(Equal(uint(7000 + i)))
				}
			})
		})

		When("port range is exhausted", func() {
			BeforeEach(func() {
				drbdCfg.MaxPort = drbdCfg.MinPort // Only one port available

				rvrList = rvrList[:2]
				// Set first RVR to use the only available port
				rvrList[0].Status.DRBD.Config.Address.IPv4 = "192.168.1.10"
				rvrList[0].Status.DRBD.Config.Address.Port = drbdCfg.MinPort
			})

			It("should set condition to false with NoFreePortAvailable reason", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

				By("verifying second RVR has error condition")
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[1]), &rvrList[1])).To(Succeed())
				Expect(rvrList[1].Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", Equal(v1alpha1.RVRCondAddressConfiguredType)),
					HaveField("Status", Equal(metav1.ConditionFalse)),
					HaveField("Reason", Equal(v1alpha1.RVRCondAddressConfiguredReasonNoFreePortAvailable)),
				)))
			})
		})

	})
})

// HaveUniquePorts returns a matcher that checks if all RVRs have unique ports set.
func HaveUniquePorts() gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(list []v1alpha1.ReplicatedVolumeReplica) (bool, error) {
		result := make(map[uint]struct{}, len(list))
		for i := range list {
			if list[i].Status.DRBD == nil ||
				list[i].Status.DRBD.Config == nil ||
				list[i].Status.DRBD.Config.Address == nil {
				return false, fmt.Errorf("item %d does not have port", i)
			}
			result[list[i].Status.DRBD.Config.Address.Port] = struct{}{}
		}
		return len(result) == len(list), nil
	}).WithMessage("Ports need to be set and unique")
}

type testDRBDConfig struct {
	MinPort uint
	MaxPort uint
}

func (d testDRBDConfig) IsPortValid(port uint) bool {
	return rvrstatusconfigaddress.IsPortValid(d, port)
}

func (d testDRBDConfig) DRBDMaxPort() uint {
	return d.MaxPort
}

func (d testDRBDConfig) DRBDMinPort() uint {
	return d.MinPort
}

var _ rvrstatusconfigaddress.DRBDConfig = testDRBDConfig{}
