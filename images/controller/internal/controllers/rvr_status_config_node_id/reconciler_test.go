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

package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvrstatusconfignodeid.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha3.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrstatusconfignodeid.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).NotTo(Requeue(), "should ignore NotFound errors")
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha3.ReplicatedVolume) error {
				return internalServerError
			}))
		})

		It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).Error().To(MatchError(internalServerError), "should return error when Get fails")
		})
	})

	When("RV with RVR created", func() {
		// Base RV and RVRs created in BeforeEach, can be modified in child tests
		var (
			rv       *v1alpha3.ReplicatedVolume
			rvr      *v1alpha3.ReplicatedVolumeReplica
			otherRV  *v1alpha3.ReplicatedVolume
			otherRVR *v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			// Base RV for volume-1 - used in most tests
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-1",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-1",
					NodeName:             "node-1",
					Type:                 "Diskful",
				},
			}
			Expect(controllerutil.SetControllerReference(rv, rvr, scheme)).To(Succeed())

			// Base RV and RVR for volume-2 - used for isolation tests
			otherRV = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-2",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
			otherRVR = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-vol2-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-2",
					NodeName:             "node-3",
					Type:                 "Diskful",
				},
			}
			Expect(controllerutil.SetControllerReference(otherRV, otherRVR, scheme)).To(Succeed())
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rv != nil {
				Expect(cl.Create(ctx, rv)).To(Succeed(), "should create base RV")
			}
			if rvr != nil {
				Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create base RVR")
			}
			// otherRV and otherRVR are created only in child tests when needed
		})

		DescribeTableSubtree("when rvr has",
			Entry("nil Status", func() { rvr.Status = nil }),
			Entry("nil Status.DRBD", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: nil} }),
			Entry("nil Status.DRBD.Config", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: &v1alpha3.DRBD{Config: nil}} }),
			Entry("nil Status.DRBD.Config.NodeId", func() {
				rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{NodeId: nil},
					},
				}
			}),
			func(setup func()) {
				BeforeEach(setup)

				It("should reconcile successfully and assign nodeID", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolume with RVR that has nil status fields")
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying nodeID was assigned")
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha3.RVRMinNodeID))), "first replica should get nodeID MinNodeID")
				})
			})

		When("RVR without nodeID", func() {
			BeforeEach(func() {
				rvr.Status = nil
			})

			It("assigns nodeID to first replica", func(ctx SpecContext) {
				By("Reconciling RV with first replica without nodeID")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

				By("Verifying first replica got nodeID MinNodeID and it is actually assigned")
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
				Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha3.RVRMinNodeID))), "first replica should get nodeID MinNodeID")
			})

			When("multiple RVRs exist", func() {
				var rvrList []*v1alpha3.ReplicatedVolumeReplica

				JustBeforeEach(func(ctx SpecContext) {
					for i := range rvrList {
						Expect(cl.Create(ctx, rvrList[i])).To(Succeed(), "should create RVR successfully")
					}
				})

				When("assigning nodeID sequentially", func() {

					BeforeEach(func() {
						rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, 6)
						for i := 0; i < 5; i++ {
							nodeID := v1alpha3.RVRMinNodeID + uint(i)
							rvrList[i] = &v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-seq-%d", i+1),
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             fmt.Sprintf("node-%d", i+1),
									Type:                 "Diskful",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{
											NodeId: &nodeID,
										},
									},
								},
							}
							Expect(controllerutil.SetControllerReference(rv, rvrList[i], scheme)).To(Succeed())
						}
						// Add one more without nodeId - should get next sequential nodeID
						rvrList[5] = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-seq-6",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-6",
								Type:                 "Diskful",
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrList[5], scheme)).To(Succeed())
					})

					It("assigns nodeID sequentially and ensures uniqueness", func(ctx SpecContext) {
						By("Reconciling RV with replica without nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying sequential assignment: next available nodeID after 0-4")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[5]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha3.RVRMinNodeID+5))), "should assign nodeID MinNodeID+5 as next sequential value")
					})
				})

				When("isolating nodeIDs by volume", func() {
					BeforeEach(func() {
						nodeID1 := v1alpha3.RVRMinNodeID
						nodeID2 := v1alpha3.RVRMinNodeID + 1
						rvr1 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-vol1-1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID1},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr1, scheme)).To(Succeed())
						rvr2 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-vol1-2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-2",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID2},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr2, scheme)).To(Succeed())
						rvrList = []*v1alpha3.ReplicatedVolumeReplica{rvr1, rvr2}
					})

					JustBeforeEach(func(ctx SpecContext) {
						Expect(cl.Create(ctx, otherRV)).To(Succeed(), "should create RV for volume-2")
						Expect(cl.Create(ctx, otherRVR)).To(Succeed(), "should create RVR for volume-2")
					})

					It("isolates nodeIDs by volume", func(ctx SpecContext) {
						By("Reconciling RV in volume-2")
						Expect(rec.Reconcile(ctx, RequestFor(otherRV))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying volume-2 is independent: should get nodeID MinNodeID")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(otherRVR), otherRVR)).To(Succeed(), "should get updated RVR")
						Expect(otherRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha3.RVRMinNodeID))), "volume-2 should get nodeID MinNodeID independently of volume-1")
					})
				})

				When("filling gaps in nodeIDs", func() {
					var rvrWithoutNodeID1 *v1alpha3.ReplicatedVolumeReplica
					var rvrWithoutNodeID2 *v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						// Don't create parent rvr for this test - we'll create all RVRs ourselves
						rvr = nil
						nodeID0 := v1alpha3.RVRMinNodeID
						nodeID2 := v1alpha3.RVRMinNodeID + 2
						nodeID3 := v1alpha3.RVRMinNodeID + 3
						rvr1 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-gap-1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID0},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr1, scheme)).To(Succeed())
						rvr2 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-gap-2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-2",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID2},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr2, scheme)).To(Succeed())
						rvr3 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-gap-3",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-3",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID3},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr3, scheme)).To(Succeed())
						rvrWithoutNodeID1 = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-gap-4",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-4",
								Type:                 "Diskful",
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID1, scheme)).To(Succeed())
						rvrWithoutNodeID2 = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-gap-5",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-5",
								Type:                 "Diskful",
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID2, scheme)).To(Succeed())
						rvrList = []*v1alpha3.ReplicatedVolumeReplica{rvr1, rvr2, rvr3, rvrWithoutNodeID1, rvrWithoutNodeID2}
					})

					It("fills gaps in nodeIDs and assigns unique nodeIDs in parallel", func(ctx SpecContext) {
						By("Reconciling RV with multiple replicas without nodeID to test gap filling and parallel processing")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying gap filling: both RVRs should get unique nodeIDs (MinNodeID+1 and MinNodeID+4)")
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID1), updatedRVR1)).To(Succeed(), "should get updated RVR1")
						updatedRVR2 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID2), updatedRVR2)).To(Succeed(), "should get updated RVR2")

						// Both should get valid nodeIDs
						Expect(updatedRVR1).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha3.RVRMinNodeID))), "RVR1 should get valid nodeID")
						Expect(updatedRVR1).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("<=", v1alpha3.RVRMaxNodeID))), "RVR1 should get valid nodeID")
						Expect(updatedRVR2).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha3.RVRMinNodeID))), "RVR2 should get valid nodeID")
						Expect(updatedRVR2).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("<=", v1alpha3.RVRMaxNodeID))), "RVR2 should get valid nodeID")

						// NodeIDs should be unique
						Expect(*updatedRVR1.Status.DRBD.Config.NodeId).NotTo(Equal(*updatedRVR2.Status.DRBD.Config.NodeId), "nodeIDs should be unique")

						// One should get MinNodeID+1 (gap filler), another should get MinNodeID+4 (next available after 0,2,3)
						expectedNodeIDs := []uint{v1alpha3.RVRMinNodeID + 1, v1alpha3.RVRMinNodeID + 4}
						Expect(*updatedRVR1.Status.DRBD.Config.NodeId).To(BeElementOf(expectedNodeIDs), "RVR1 should get one of the expected nodeIDs (MinNodeID+1 or MinNodeID+4)")
						Expect(*updatedRVR2.Status.DRBD.Config.NodeId).To(BeElementOf(expectedNodeIDs), "RVR2 should get one of the expected nodeIDs (MinNodeID+1 or MinNodeID+4)")
					})
				})

				When("nodeID already assigned", func() {
					var testRVR *v1alpha3.ReplicatedVolumeReplica
					var testNodeID uint

					BeforeEach(func() {
						testNodeID = v1alpha3.RVRMinNodeID + 3
						testRVR = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-idemp-1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &testNodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, testRVR, scheme)).To(Succeed())
						rvrList = []*v1alpha3.ReplicatedVolumeReplica{testRVR}
					})

					It("does not reassign nodeID if already assigned", func(ctx SpecContext) {
						By("First reconciliation: should not reassign existing nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when nodeID already assigned")

						By("Verifying nodeID remains unchanged after first reconciliation")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRVR), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", testNodeID))), "nodeID should remain unchanged, not be reassigned")

						By("Second reconciliation: should still not reassign (idempotent)")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when nodeID already assigned")

						By("Verifying nodeID hasn't changed after both reconciliations")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRVR), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", testNodeID))), "nodeID should remain unchanged after multiple reconciliations (idempotent)")
					})
				})

				When("ignoring invalid nodeID", func() {
					var rvrWithoutNodeID *v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						invalidNodeID := v1alpha3.RVRMaxNodeID + 1
						rvr1 := &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-invalid-1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvr1, scheme)).To(Succeed())
						rvrWithoutNodeID = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-invalid-2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-2",
								Type:                 "Diskful",
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID, scheme)).To(Succeed())
						rvrList = []*v1alpha3.ReplicatedVolumeReplica{rvr1, rvrWithoutNodeID}
					})

					It("ignores nodeID outside valid range", func(ctx SpecContext) {
						By("Reconciling RV with replica without nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying invalid nodeID was ignored: should get nodeID MinNodeID")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha3.RVRMinNodeID))), "should get nodeID MinNodeID because invalid nodeID was ignored")
					})
				})

				When("reassigning invalid nodeID", func() {
					var rvrWithInvalidNodeID *v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						invalidNodeID := v1alpha3.RVRMaxNodeID + 1
						rvrWithInvalidNodeID = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-reassign-1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrWithInvalidNodeID, scheme)).To(Succeed())
						rvrList = []*v1alpha3.ReplicatedVolumeReplica{rvrWithInvalidNodeID}
					})

					It("logs warning and reassigns when nodeID > MaxNodeID is already set", func(ctx SpecContext) {
						By("Reconciling RV: should log warning about invalid nodeID and reassign valid one")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful reassignment")

						By("Verifying invalid nodeID was reset and valid nodeID was assigned")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithInvalidNodeID), updatedRVR)).To(Succeed(), "should get updated RVR")
						By("Checking that assigned nodeID is within valid range")
						validNodeIDs := []uint{
							v1alpha3.RVRMinNodeID,
							v1alpha3.RVRMinNodeID + 1,
							v1alpha3.RVRMinNodeID + 2,
						}
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha3.RVRMinNodeID))), "should assign valid nodeID >= MinNodeID")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("<=", v1alpha3.RVRMaxNodeID))), "should assign valid nodeID <= MaxNodeID")
						Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(BeElementOf(validNodeIDs), "should assign one of the first available valid nodeIDs")
					})
				})

				When("resetting invalid nodeID", func() {
					var rvrWithInvalidNodeID *v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						// Don't create parent rvr for this test - we'll create all RVRs ourselves
						rvr = nil
						// Create 6 replicas with valid nodeIDs (MinNodeID+1 to MinNodeID+6), leaving nodeID MaxNodeID free
						rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, 7)
						for i := 1; i < 7; i++ {
							nodeID := v1alpha3.RVRMinNodeID + uint(i)
							rvrList[i-1] = &v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-reset-%d", i+1),
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             fmt.Sprintf("node-%d", i+1),
									Type:                 "Diskful",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID},
									},
								},
							}
							Expect(controllerutil.SetControllerReference(rv, rvrList[i-1], scheme)).To(Succeed())
						}
						// Create replica with invalid nodeID > MaxNodeID
						invalidNodeID := v1alpha3.RVRMaxNodeID + 1
						rvrWithInvalidNodeID = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-reset-invalid",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-invalid",
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrWithInvalidNodeID, scheme)).To(Succeed())
						rvrList[6] = rvrWithInvalidNodeID
					})

					It("resets invalid nodeID and reassigns valid one", func(ctx SpecContext) {
						By("Reconciling RV: should detect invalid nodeID, reset it, and assign free nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful reassignment")

						By("Verifying invalid nodeID was reset and valid nodeID was assigned")
						updatedRVRInvalid := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithInvalidNodeID), updatedRVRInvalid)).To(Succeed(), "should get updated RVR")
						// Should assign one of the free nodeIDs (MinNodeID=0 or MaxNodeID=7)
						expectedNodeIDs := []uint{v1alpha3.RVRMinNodeID, v1alpha3.RVRMaxNodeID}
						Expect(updatedRVRInvalid).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeElementOf(expectedNodeIDs))), "should assign free nodeID (MinNodeID or MaxNodeID) after resetting invalid nodeID")
						Expect(updatedRVRInvalid).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha3.RVRMinNodeID))), "nodeID should be >= MinNodeID")
						Expect(updatedRVRInvalid).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("<=", v1alpha3.RVRMaxNodeID))), "nodeID should be <= MaxNodeID")
					})
				})

				When("List fails", func() {
					listError := errors.New("failed to list replicas")
					BeforeEach(func() {
						rvrList = nil // Reset rvrList for this test
						clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
							List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
								if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
									return listError
								}
								return cl.List(ctx, list, opts...)
							},
						})
					})

					It("should fail if listing replicas failed", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(listError), "should return error when List fails")
					})
				})
			})

			When("all nodeIDs are used", func() {
				var rvrList []*v1alpha3.ReplicatedVolumeReplica
				var rvr9 *v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					// Don't create parent rvr for this test - we'll create all RVRs ourselves
					rvr = nil
					// Create replicas using all nodeIds from MinNodeID to MaxNodeID
					totalNodeIDs := int(v1alpha3.RVRMaxNodeID - v1alpha3.RVRMinNodeID + 1)
					rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, totalNodeIDs)
					for i := 0; i < totalNodeIDs; i++ {
						nodeID := v1alpha3.RVRMinNodeID + uint(i)
						rvrList[i] = &v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("rvr-allused-%d", i+1),
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             fmt.Sprintf("node-%d", i+1),
								Type:                 "Diskful",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrList[i], scheme)).To(Succeed())
					}
					// Create next RVR without nodeID - should fail (all nodeIDs used)
					rvr9 = &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-allused-9",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-9",
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr9, scheme)).To(Succeed())
				})

				JustBeforeEach(func(ctx SpecContext) {
					for i := range rvrList {
						Expect(cl.Create(ctx, rvrList[i])).To(Succeed(), "should create RVR with nodeID")
					}
					Expect(cl.Create(ctx, rvr9)).To(Succeed(), "should create RVR without nodeID")
				})

				It("returns error when all nodeIDs are used and assigns freed nodeID after deletion", func(ctx SpecContext) {
					By("Reconciling RV should fail when all nodeIDs are used")
					Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(e.ErrInvalidCluster), "should return ErrInvalidCluster when all nodeIDs are used")

					By("Deleting one RVR to free its nodeID")
					freedNodeID := v1alpha3.RVRMinNodeID + 3
					Expect(cl.Delete(ctx, rvrList[3])).To(Succeed(), "should delete RVR successfully")

					By("Reconciling RV again: should now get the freed nodeID")
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying freed nodeID was assigned")
					updatedRVR9 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr9), updatedRVR9)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR9).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", freedNodeID))), "should assign freed nodeID after deletion")
				})
			})
		})

		When("Patch fails with non-NotFound error", func() {
			patchError := errors.New("failed to patch status")
			BeforeEach(func() {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
							if subResourceName == "status" {
								return patchError
							}
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("should fail if patching ReplicatedVolumeReplica status failed with non-NotFound error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(patchError), "should return error when Patch fails")
			})
		})

		When("Patch fails with 409 Conflict", func() {
			var conflictError error
			var patchAttempts int

			BeforeEach(func() {
				patchAttempts = 0
				// Simulate 409 Conflict error (e.g., if another controller updates the same resource)
				conflictError = kerrors.NewConflict(
					schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumereplicas"},
					rvr.Name,
					errors.New("resourceVersion conflict: the object has been modified"),
				)
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if rvrObj, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
							if subResourceName == "status" && rvrObj.Name == rvr.Name {
								patchAttempts++
								// Simulate conflict on first patch attempt only
								if patchAttempts == 1 {
									return conflictError
								}
								// Allow subsequent attempts to succeed (simulating retry after conflict)
							}
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("should return error on 409 Conflict and succeed on retry", func(ctx SpecContext) {
				By("First reconcile: should fail with 409 Conflict")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(conflictError), "should return conflict error on first attempt")

				By("Second reconcile (retry): should succeed after conflict resolved")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")

				By("Verifying nodeID was assigned after retry")
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
				Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha3.RVRMinNodeID))), "nodeID should be assigned")
			})
		})

	})
})
