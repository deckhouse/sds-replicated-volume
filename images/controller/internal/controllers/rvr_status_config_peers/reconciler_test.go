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

// cspell:words Diskless Logr Subresource apimachinery gomega gvks metav onsi

package rvr_status_config_peers_test

import (
	"context"
	"errors"
	"fmt"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types"     // cspell:words gomegatypes
	apierrors "k8s.io/apimachinery/pkg/api/errors" // cspell:words apierrors
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil" // cspell:words controllerutil
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_peers"
)

// HaveNoPeers is a Gomega matcher that checks a single RVR has no peers
func HaveNoPeers() gomegatypes.GomegaMatcher {
	return SatisfyAny(
		HaveField("Status", BeNil()),
		HaveField("Status.DRBD", BeNil()),
		HaveField("Status.DRBD.Config", BeNil()),
		HaveField("Status.DRBD.Config.Peers", BeEmpty()),
	)
}

// HaveAllPeersSet is a matcher factory that returns a Gomega matcher for a single RVR
// It checks that the RVR has all other RVRs from expectedResources as peers but his own
func HaveAllPeersSet(expectedPeerReplicas []v1alpha3.ReplicatedVolumeReplica) gomegatypes.GomegaMatcher {
	if len(expectedPeerReplicas) < 2 {
		return HaveNoPeers()
	}
	expectedPeers := make(map[string]v1alpha3.Peer, len(expectedPeerReplicas)-1)
	for _, rvr := range expectedPeerReplicas {
		if rvr.Status == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status, but it's nil")
		}

		if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			return gcustom.MakeMatcher(func(_ any) bool { return false }).
				WithMessage("expected rvr to have status.drbd.config, but it's nil")
		}
		diskless := rvr.Status.DRBD.Config.Disk == ""
		expectedPeers[rvr.Spec.NodeName] = v1alpha3.Peer{
			NodeId:   *rvr.Status.DRBD.Config.NodeId,
			Address:  *rvr.Status.DRBD.Config.Address,
			Diskless: diskless,
		}
	}
	return SatisfyAll(
		HaveField("Status.DRBD.Config.Peers", HaveLen(len(expectedPeerReplicas)-1)),
		WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) map[string]v1alpha3.Peer {
			ret := maps.Clone(rvr.Status.DRBD.Config.Peers)
			diskless := rvr.Status.DRBD.Config.Disk == ""
			ret[rvr.Spec.NodeName] = v1alpha3.Peer{
				NodeId:   *rvr.Status.DRBD.Config.NodeId,
				Address:  *rvr.Status.DRBD.Config.Address,
				Diskless: diskless,
			}
			return ret
		}, Equal(expectedPeers)),
	)
}

// makeReady sets up an RVR to be in ready state by initializing Status and DRBD.Config with NodeId and Address
func makeReady(rvr *v1alpha3.ReplicatedVolumeReplica, nodeID uint, address v1alpha3.Address) {
	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha3.DRBD{}
	}

	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
	}

	rvr.Status.DRBD.Config.NodeId = &nodeID
	rvr.Status.DRBD.Config.Address = &address
}

// BeReady returns a matcher that checks if an RVR is in ready state (has NodeName, NodeId, and Address)
func BeReady() gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Spec.NodeName", Not(BeEmpty())),
		HaveField("Status.DRBD.Config.NodeId", Not(BeNil())),
		HaveField("Status.DRBD.Config.Address", Not(BeNil())),
	)
}

var _ = Describe("Reconciler", func() {
	var cl client.WithWatch
	var rec *rvr_status_config_peers.Reconciler
	var scheme *runtime.Scheme
	var interceptorFuncs interceptor.Funcs

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
		interceptorFuncs = interceptor.Funcs{}
	})

	JustBeforeEach(func() {
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha3.ReplicatedVolumeReplica{},
				&v1alpha3.ReplicatedVolume{}).
			WithInterceptorFuncs(interceptorFuncs).
			Build()
		rec = rvr_status_config_peers.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: "not-existing-rv",
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			interceptorFuncs = interceptor.Funcs{
				Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
						return internalServerError
					}
					return client.Get(ctx, key, obj, opts...)
				},
			}
		})

		It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).Error().To(MatchError(internalServerError))
		})
	})

	When("ReplicatedVolume created", func() {
		var rv, otherRv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rv",
					UID:  "test-uid",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}

			otherRv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-rv",
					UID:  "other-uid",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed())
			Expect(cl.Create(ctx, otherRv)).To(Succeed())
		})

		expectReconcileSuccessfully := func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: rv.Name,
				},
			})).To(Equal(reconcile.Result{}))
		}

		When("first replica created", func() {
			var firstRvr v1alpha3.ReplicatedVolumeReplica

			BeforeEach(func() {
				firstRvr = v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
					Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
				}
				Expect(controllerutil.SetControllerReference(rv, &firstRvr, scheme)).To(Succeed())
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, &firstRvr)).To(Succeed())
			})

			It("should not have peers", func(ctx SpecContext) {
				expectReconcileSuccessfully(ctx)
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&firstRvr), &firstRvr)).To(Succeed())
				Expect(firstRvr).To(HaveNoPeers())
			})

			When("List fails", func() {
				listError := errors.New("failed to list replicas")
				BeforeEach(func() {
					interceptorFuncs = interceptor.Funcs{
						List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
								return listError
							}
							return client.List(ctx, list, opts...)
						},
					}
				})

				It("should fail if listing replicas failed", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: rv.Name},
					})).Error().To(MatchError(listError))
				})
			})

			Context("if rvr-1 is ready", func() {
				BeforeEach(func() {
					makeReady(&firstRvr, 1, v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000})
				})

				It("should have no peers", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&firstRvr), &firstRvr)).To(Succeed())
					Expect(firstRvr).To(HaveNoPeers())
				})

				When("second replica created", func() {
					var secondRvr v1alpha3.ReplicatedVolumeReplica
					BeforeEach(func() {
						secondRvr = v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "test-rv",
								NodeName:             "node-2"},
						}
						Expect(controllerutil.SetControllerReference(rv, &secondRvr, scheme)).To(Succeed())
					})

					JustBeforeEach(func(ctx SpecContext) {
						Expect(cl.Create(ctx, &secondRvr)).To(Succeed())
					})

					It("rvr-1 should have no peers", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&firstRvr), &firstRvr)).To(Succeed())
						Expect(firstRvr).To(HaveNoPeers())
					})

					It("rvr-2 should have no peers", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&secondRvr), &secondRvr)).To(Succeed())
						Expect(secondRvr).To(HaveNoPeers())
					})

					Context("if rvr-2 ready", func() {
						BeforeEach(func() {
							makeReady(&secondRvr, 2, v1alpha3.Address{IPv4: "192.168.1.4", Port: 7001})
						})

						It("should update peers when RVR transitions to ready state", func(ctx SpecContext) {
							expectReconcileSuccessfully(ctx)

							Expect(cl.Get(ctx, client.ObjectKeyFromObject(&firstRvr), &firstRvr)).To(Succeed())
							Expect(cl.Get(ctx, client.ObjectKeyFromObject(&secondRvr), &secondRvr)).To(Succeed())
							list := []v1alpha3.ReplicatedVolumeReplica{firstRvr, secondRvr}
							Expect(list).To(HaveEach(HaveAllPeersSet(list)))
						})

						When("Patch fails with non-NotFound error", func() {
							patchError := errors.New("failed to patch status")
							BeforeEach(func() {
								interceptorFuncs = interceptor.Funcs{
									SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
										if _, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
											if subResourceName == "status" {
												return patchError
											}
										}
										return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
									},
								}
							})

							It("should fail if patching ReplicatedVolumeReplica status failed with non-NotFound error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, reconcile.Request{
									NamespacedName: types.NamespacedName{Name: rv.Name},
								})).Error().To(MatchError(patchError))
							})
						})

						When("Patch fails with NotFound error", func() {
							BeforeEach(func() {
								interceptorFuncs = interceptor.Funcs{
									SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
										if rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
											if subResourceName == "status" && rvr.Name == "rvr-1" {
												return apierrors.NewNotFound(schema.GroupResource{Resource: "replicatedvolumereplicas"}, rvr.Name)
											}
										}
										return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
									},
								}
							})

							It("should return no error if patching ReplicatedVolumeReplica status failed with NotFound error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, reconcile.Request{
									NamespacedName: types.NamespacedName{Name: rv.Name},
								})).To(Equal(reconcile.Result{}))
							})
						})

						DescribeTableSubtree("if rvr-2 is",
							Entry("without status", func() { secondRvr.Status = nil }),
							Entry("without status.drbd", func() { secondRvr.Status.DRBD = nil }),
							Entry("without status.drbd.config", func() { secondRvr.Status.DRBD.Config = nil }),
							Entry("without address", func() { secondRvr.Status.DRBD.Config.Address = nil }),
							Entry("without nodeId", func() { secondRvr.Status.DRBD.Config.NodeId = nil }),
							Entry("without nodeName", func() { secondRvr.Spec.NodeName = "" }),
							Entry("without owner reference", func() { secondRvr.OwnerReferences = []metav1.OwnerReference{} }),
							Entry("with other owner reference", func() {
								secondRvr.OwnerReferences = []metav1.OwnerReference{}
								Expect(controllerutil.SetControllerReference(otherRv, &secondRvr, scheme)).To(Succeed())
							}), func(setup func()) {
								BeforeEach(func() {
									setup()
								})

								JustBeforeEach(func(ctx SpecContext) {
									expectReconcileSuccessfully(ctx)
								})

								It("rvr-1 should have no peers", func(ctx SpecContext) {
									Expect(cl.Get(ctx, client.ObjectKeyFromObject(&firstRvr), &firstRvr)).To(Succeed())
									Expect(firstRvr).To(HaveNoPeers())
								})

								It("rvr-2 should have no peers", func(ctx SpecContext) {
									Expect(cl.Get(ctx, client.ObjectKeyFromObject(&secondRvr), &secondRvr)).To(Succeed())
									Expect(secondRvr).To(HaveNoPeers())
								})
							})
					})
				})
			})
		})

		When("few replicas created", func() {
			var rvrList []v1alpha3.ReplicatedVolumeReplica

			getAll := func(ctx context.Context, rvrList []v1alpha3.ReplicatedVolumeReplica) {
				for i := range rvrList {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[i]), &rvrList[i])).To(Succeed())
				}
			}

			BeforeEach(func() {
				rvrList = []v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
						Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
						Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-3"},
						Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-3"},
					},
				}

				for i := range rvrList {
					Expect(controllerutil.SetControllerReference(rv, &rvrList[i], scheme)).To(Succeed())
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				for i := range rvrList {
					Expect(cl.Create(ctx, &rvrList[i])).To(Succeed())
				}
			})

			Context("if first replica ready", func() {
				BeforeEach(func() {
					if len(rvrList) == 0 {
						Skip("empty rvrList")
					}
					makeReady(&rvrList[0], uint(1), v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000})
				})

				It("should not have any peers", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					getAll(ctx, rvrList)
					Expect(rvrList).To(HaveEach(HaveNoPeers()))
				})

				When("all the rest becomes ready", func() {
					JustBeforeEach(func(ctx SpecContext) {
						for i, rvr := range rvrList[1:] {
							By(fmt.Sprintf("Making ready %s", rvr.Name))
							makeReady(
								&rvr,
								uint(i),
								v1alpha3.Address{IPv4: fmt.Sprintf("192.168.1.%d", i+1), Port: 7000 + uint(i)},
							)
							Expect(cl.Status().Update(ctx, &rvr)).To(Succeed())
						}
					})

					It("should have all peers set", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)
						getAll(ctx, rvrList)
						Expect(rvrList).To(HaveEach(HaveAllPeersSet(rvrList)))
					})
				})
			})

			Context("if all replicas ready", func() {
				BeforeEach(func() {
					for i := range rvrList {
						makeReady(
							&rvrList[i],
							uint(i),
							v1alpha3.Address{IPv4: fmt.Sprintf("192.168.1.%d", i+1), Port: 7000 + uint(i)},
						)
					}
				})

				It("should have all peers set", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					getAll(ctx, rvrList)
					Expect(rvrList).To(HaveEach(HaveAllPeersSet(rvrList)))
				})

				It("should remove deleted RVR from peers of remaining RVRs", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					Expect(cl.Delete(ctx, &rvrList[0])).To(Succeed())

					expectReconcileSuccessfully(ctx)
					list := rvrList[1:]

					getAll(ctx, list)
					Expect(list).To(HaveEach(HaveAllPeersSet(list)))
				})

				When("multiple RVRs exist on same node", func() {
					BeforeEach(func() {
						// Use all 3 RVRs, but set node-2 to node-1 for rvr-2
						rvrList[1].Spec.NodeName = "node-1" // Same node as rvr-1
						nodeID1 := uint(1)
						nodeID2 := uint(2)
						nodeID3 := uint(3)
						address1 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000}
						address2 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7001} // Same IP, different port
						address3 := v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000}
						if rvrList[0].Status == nil {
							rvrList[0].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
						}
						if rvrList[0].Status.DRBD == nil {
							rvrList[0].Status.DRBD = &v1alpha3.DRBD{}
						}
						if rvrList[0].Status.DRBD.Config == nil {
							rvrList[0].Status.DRBD.Config = &v1alpha3.DRBDConfig{}
						}
						if rvrList[1].Status == nil {
							rvrList[1].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
						}
						if rvrList[1].Status.DRBD == nil {
							rvrList[1].Status.DRBD = &v1alpha3.DRBD{}
						}
						if rvrList[1].Status.DRBD.Config == nil {
							rvrList[1].Status.DRBD.Config = &v1alpha3.DRBDConfig{}
						}
						if rvrList[2].Status == nil {
							rvrList[2].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
						}
						if rvrList[2].Status.DRBD == nil {
							rvrList[2].Status.DRBD = &v1alpha3.DRBD{}
						}
						if rvrList[2].Status.DRBD.Config == nil {
							rvrList[2].Status.DRBD.Config = &v1alpha3.DRBDConfig{}
						}
						rvrList[0].Status.DRBD.Config.NodeId = &nodeID1
						rvrList[0].Status.DRBD.Config.Address = &address1
						rvrList[1].Status.DRBD.Config.NodeId = &nodeID2
						rvrList[1].Status.DRBD.Config.Address = &address2
						rvrList[2].Status.DRBD.Config.NodeId = &nodeID3
						rvrList[2].Status.DRBD.Config.Address = &address3
					})

					It("should only keep one peer entry per node", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)

						// rvr3 should only have one peer entry for node-1 (the first one found)
						updatedRVR3 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR3)).To(Succeed())
						Expect(updatedRVR3.Status.DRBD.Config.Peers).To(And(
							HaveKey("node-1"),
							HaveLen(1),
						))
					})
				})

				When("peers are already correct", func() {
					BeforeEach(func() {
						// Use only first 2 RVRs
						rvrList = rvrList[:2]
					})

					It("should not update if peers are unchanged", func(ctx SpecContext) {
						// First reconcile
						expectReconcileSuccessfully(ctx)

						// Get the state after first reconcile
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
						initialPeers := updatedRVR1.Status.DRBD.Config.Peers

						// Second reconcile - should not change
						expectReconcileSuccessfully(ctx)

						// Verify peers are unchanged
						updatedRVR1After := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1After)).To(Succeed())
						Expect(updatedRVR1After.Status.DRBD.Config.Peers).To(Equal(initialPeers))
						Expect(updatedRVR1After.Generation).To(Equal(updatedRVR1.Generation))
					})
				})

				Context("with diskless RVRs", func() {
					BeforeEach(func() {
						// Use only first 2 RVRs, set second one as diskless (empty disk)
						rvrList = rvrList[:2]
						if rvrList[1].Status == nil {
							rvrList[1].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
						}
						if rvrList[1].Status.DRBD == nil {
							rvrList[1].Status.DRBD = &v1alpha3.DRBD{}
						}
						if rvrList[1].Status.DRBD.Config == nil {
							rvrList[1].Status.DRBD.Config = &v1alpha3.DRBDConfig{}
						}
						// Empty disk means diskless
						rvrList[1].Status.DRBD.Config.Disk = ""
					})

					It("should include diskless flag in peer information", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)

						// Verify rvr1 has rvr2 with diskless flag
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
						Expect(updatedRVR1.Status.DRBD.Config.Peers).To(HaveKeyWithValue("node-2", HaveField("Diskless", BeTrue())))
					})
				})
			})
		})
	})
})
