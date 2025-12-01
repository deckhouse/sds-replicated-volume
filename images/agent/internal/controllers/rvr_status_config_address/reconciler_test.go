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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
	rvrstatusconfigaddress "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/rvr_status_config_address"
)

var _ = Describe("Reconciler", func() {
	var (
		cl        client.Client
		rec       *rvrstatusconfigaddress.Reconciler
		log       logr.Logger
		node      *corev1.Node
		configMap *corev1.ConfigMap
		s         *runtime.Scheme
	)

	BeforeEach(func() {
		cl = nil
		log = logr.Discard()

		// Setup scheme
		s = scheme.Scheme
		_ = metav1.AddMetaToScheme(s)
		_ = corev1.AddToScheme(s)
		_ = v1alpha3.AddToScheme(s)

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

		// Create test ConfigMap with port settings
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.ConfigMapName,
				Namespace: cluster.ConfigMapNamespace,
			},
			Data: map[string]string{
				"drbdMinPort": "7000",
				"drbdMaxPort": "9000",
			},
		}
	})

	JustBeforeEach(func(ctx SpecContext) {
		// Create fake client with status subresource support
		cl = fake.NewClientBuilder().
			WithScheme(s).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
			Build()

		// Create reconciler using New method
		rec = rvrstatusconfigaddress.NewReconciler(cl, log)

		// Create default objects if they are set
		if node != nil {
			Expect(cl.Create(ctx, node)).To(Succeed())
		}
		if configMap != nil {
			Expect(cl.Create(ctx, configMap)).To(Succeed())
		}
	})

	It("should return no error when node does not exist (ignore not found)", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "non-existent-node"}})).
			ToNot(Requeue())
	})

	When("node is missing InternalIP", func() {
		DescribeTableSubtree("when node has no status or addresses",
			Entry("has no status", func() {
				node.Status = corev1.NodeStatus{}
			}),
			Entry("has no addresses", func() {
				node.Status.Addresses = []corev1.NodeAddress{}
			}),
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should return error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(node))).Error().
						To(MatchError(rvrstatusconfigaddress.ErrNodeMissingInternalIP))
				})
			})

		DescribeTableSubtree("when node has address of different type",
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

	})

	DescribeTableSubtree("should return error when ConfigMap",
		Entry("does not exist", func() {
			configMap = nil
		}),
		Entry("has wrong name", func() {
			configMap.Name = "wrong-name"
		}),
		Entry("has wrong namespace", func() {
			configMap.Namespace = "wrong-namespace"
		}),
		Entry("has invalid min port", func() {
			configMap.Data["drbdMinPort"] = "invalid"
		}),
		Entry("has invalid max port", func() {
			configMap.Data["drbdMaxPort"] = "invalid"
		}),
		Entry("has empty min port", func() {
			configMap.Data["drbdMinPort"] = ""
		}),
		Entry("has empty max port", func() {
			configMap.Data["drbdMaxPort"] = ""
		}),
		Entry("has nil Data", func() {
			configMap.Data = nil
		}),
		Entry("has missing drbdMinPort key", func() {
			delete(configMap.Data, "drbdMinPort")
		}),
		Entry("has missing drbdMaxPort key", func() {
			delete(configMap.Data, "drbdMaxPort")
		}),
		func(beforeEach func()) {
			BeforeEach(beforeEach)

			It("should return error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(node))).Error().To(MatchError(rvrstatusconfigaddress.ErrConfigSettings))
			})
		})

	It("should succeed without errors when there are no RVRs on the node", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())
	})

	When("RVRs created", func() {
		var (
			rvList           []v1alpha3.ReplicatedVolume
			thisNodeRVRList  []v1alpha3.ReplicatedVolumeReplica
			otherNodeRVRList []v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			const count = 3

			rvList = make([]v1alpha3.ReplicatedVolume, count)
			thisNodeRVRList = make([]v1alpha3.ReplicatedVolumeReplica, count)
			otherNodeRVRList = make([]v1alpha3.ReplicatedVolumeReplica, count)

			for i := range count {
				rvList[i] = v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("test-rv-%d", i+1)},
				}

				thisNodeRVRList[i] = v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-%d-this-node", i+1)},
				}
				thisNodeRVRList[i].Spec.NodeName = node.Name
				Expect(thisNodeRVRList[i].SetReplicatedVolume(&rvList[i], s)).To(Succeed())

				otherNodeRVRList[i] = v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-%d-other-node", i+1)},
					Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "other-node"},
				}
				Expect(otherNodeRVRList[i].SetReplicatedVolume(&rvList[i], s)).To(Succeed())
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			for i := range rvList {
				Expect(cl.Create(ctx, &rvList[i])).To(Succeed())
			}
			for i := range thisNodeRVRList {
				Expect(cl.Create(ctx, &thisNodeRVRList[i])).To(Succeed())
			}
			for i := range otherNodeRVRList {
				Expect(cl.Create(ctx, &otherNodeRVRList[i])).To(Succeed())
			}
		})

		It("should configure addresses for RVRs on this node", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify all RVRs on this node were updated
			for i := range thisNodeRVRList {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[i]), &thisNodeRVRList[i])).To(Succeed())
				Expect(thisNodeRVRList[i]).To(SatisfyAll(
					HaveField("Status.DRBD.Config.Address.IPv4", Equal("192.168.1.10")),
					HaveField("Status.DRBD.Config.Address.Port", BeNumerically(">=", uint(7000))),
				))
			}
		})

		It("should filter out RVRs on other nodes and not configure addresses", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify all RVRs on other nodes were not modified
			for i := range otherNodeRVRList {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&otherNodeRVRList[i]), &otherNodeRVRList[i])).To(Succeed())
			}
			Expect(otherNodeRVRList).To(HaveEach(HaveField("Status", BeNil())))
		})

		It("should configure address with first available port", func(ctx SpecContext) {
			// Use only first RVR for this test
			originalList := thisNodeRVRList
			thisNodeRVRList = thisNodeRVRList[:1]
			rvList = rvList[:1]

			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify address was configured
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[0]), &thisNodeRVRList[0])).To(Succeed())
			Expect(thisNodeRVRList[0]).To(SatisfyAll(
				HaveField("Status.DRBD.Config.Address.IPv4", Equal("192.168.1.10")),
				HaveField("Status.DRBD.Config.Address.Port", Equal(uint(7000))),
			))

			// Verify condition was set
			Expect(thisNodeRVRList[0]).To(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", Equal(v1alpha3.ConditionTypeAddressConfigured)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
				HaveField("Reason", Equal(v1alpha3.ReasonAddressConfigurationSucceeded)),
			))))

			// Restore for other tests
			thisNodeRVRList = originalList
		})

		It("should assign sequential ports", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify all RVRs got unique ports in valid range
			for i := range thisNodeRVRList {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[i]), &thisNodeRVRList[i])).To(Succeed())
			}

			Expect(thisNodeRVRList).To(SatisfyAll(
				HaveUniquePorts(),
				HaveEach(HaveField("Status.DRBD.Config.Address.Port", SatisfyAll(
					BeNumerically(">=", 7000),
					BeNumerically("<=", 9000),
				)))))
		})

		When("RVR has wrong IP address", func() {
			BeforeEach(func() {
				thisNodeRVRList[0].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{
							Address: &v1alpha3.Address{
								IPv4: "192.168.1.99", // Wrong IP
								Port: 7500,
							},
						},
					},
				}
			})

			It("should update address", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

				// Verify all RVRs have address updated to node IP
				for i := range thisNodeRVRList {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[i]), &thisNodeRVRList[i])).To(Succeed())
					Expect(thisNodeRVRList[i].Status.DRBD.Config.Address.IPv4).To(Equal("192.168.1.10"))
				}
			})
		})

		It("should set condition to false with NoFreePortAvailable reason when port range is exhausted", func(ctx SpecContext) {
			// Update ConfigMap with very small port range
			smallRangeCM := configMap.DeepCopy()
			smallRangeCM.Data["drbdMinPort"] = "7000"
			smallRangeCM.Data["drbdMaxPort"] = "7000" // Only one port available
			smallRangeCM.ResourceVersion = ""
			Expect(cl.Delete(ctx, configMap)).To(Succeed())
			Expect(cl.Create(ctx, smallRangeCM)).To(Succeed())

			// Set first RVR to use the only available port
			thisNodeRVRList[0].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
				DRBD: &v1alpha3.DRBD{
					Config: &v1alpha3.DRBDConfig{
						Address: &v1alpha3.Address{
							IPv4: "192.168.1.10",
							Port: 7000, // Uses the only available port
						},
					},
				},
			}
			Expect(cl.Update(ctx, &thisNodeRVRList[0])).To(Succeed())

			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify second RVR has error condition
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[1]), &thisNodeRVRList[1])).To(Succeed())
			Expect(thisNodeRVRList[1].Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(v1alpha3.ConditionTypeAddressConfigured)),
				HaveField("Status", Equal(metav1.ConditionFalse)),
				HaveField("Reason", Equal(v1alpha3.ReasonNoFreePortAvailable)),
			)))
		})

		It("should create missing status fields", func(ctx SpecContext) {
			// Remove status from first RVR
			thisNodeRVRList[0].Status = nil
			Expect(cl.Update(ctx, &thisNodeRVRList[0])).To(Succeed())

			Expect(rec.Reconcile(ctx, RequestFor(node))).ToNot(Requeue())

			// Verify status structure was created
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(&thisNodeRVRList[0]), &thisNodeRVRList[0])).To(Succeed())
			Expect(thisNodeRVRList[0].Status.DRBD.Config.Address).NotTo(BeNil())
		})
	})
})

// HaveUniquePorts returns a matcher that checks if all RVRs have unique ports set.
func HaveUniquePorts() gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(list []v1alpha3.ReplicatedVolumeReplica) (bool, error) {
		result := make(map[uint]struct{}, len(list))

		for i := range list {
			if list[i].Status == nil ||
				list[i].Status.DRBD == nil ||
				list[i].Status.DRBD.Config == nil ||
				list[i].Status.DRBD.Config.Address == nil {
				return false, fmt.Errorf("item %d does not have port", i)
			}
			result[list[i].Status.DRBD.Config.Address.Port] = struct{}{}
		}
		return len(result) == len(list), nil
	}).WithMessage("Ports need to be set and unique")
}
