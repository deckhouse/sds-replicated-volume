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

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
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
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(), "should add v1alpha1 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{})
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
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha1.ReplicatedVolume) error {
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
		var (
			rv       *v1alpha1.ReplicatedVolume
			rvr      *v1alpha1.ReplicatedVolumeReplica
			otherRV  *v1alpha1.ReplicatedVolume
			otherRVR *v1alpha1.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-1",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
			rvr = &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-1",
					NodeName:             "node-1",
					Type:                 "Diskful",
				},
			}
			Expect(controllerutil.SetControllerReference(rv, rvr, scheme)).To(Succeed())

			otherRV = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-2",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
			otherRVR = &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-vol2-1",
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
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
		})

		BeforeEach(func() {
			// Initialize status structure to simplify nil field tests
			if rvr.Status == nil {
				rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
			}
			if rvr.Status.DRBD == nil {
				rvr.Status.DRBD = &v1alpha1.DRBD{}
			}
			if rvr.Status.DRBD.Config == nil {
				rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
			}
		})

		DescribeTableSubtree("when rvr has",
			Entry("nil Status", func() { rvr.Status = nil }),
			Entry("nil Status.DRBD", func() { rvr.Status.DRBD = nil }),
			Entry("nil Status.DRBD.Config", func() { rvr.Status.DRBD.Config = nil }),
			Entry("nil Status.DRBD.Config.NodeId", func() { rvr.Status.DRBD.Config.NodeId = nil }),
			func(setup func()) {
				BeforeEach(setup)

				It("should reconcile successfully and assign nodeID", func(ctx SpecContext) {
					By("Reconciling until nodeID is assigned")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolumeReplica {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed(), "should get updated RVR")
						return rvr
					}).Should(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha1.RVRMinNodeID))), "first replica should get nodeID MinNodeID")
				})
			})

		When("multiple RVRs exist", func() {
			var rvrList []*v1alpha1.ReplicatedVolumeReplica

			JustBeforeEach(func(ctx SpecContext) {
				for i := range rvrList {
					Expect(cl.Create(ctx, rvrList[i])).To(Succeed(), "should create RVR successfully")
				}
			})

			When("assigning nodeID to multiple RVRs", func() {
				const (
					// Number of RVRs with pre-assigned nodeIDs (0-4)
					numRVRsWithNodeID     = 5
					rvrWithoutNodeIDIndex = 5 // Index of RVR that needs nodeID assignment
				)

				BeforeEach(func() {
					By("Creating 5 RVRs with nodeID 0-4 and one RVR without nodeID")
					rvr = nil
					rvrList = make([]*v1alpha1.ReplicatedVolumeReplica, 6)
					for i := 0; i < numRVRsWithNodeID; i++ {
						nodeID := v1alpha1.RVRMinNodeID + uint(i)
						rvrList[i] = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("rvr-seq-%d", i+1),
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             fmt.Sprintf("node-%d", i+1),
								Type:                 "Diskful",
							},
							Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Config: &v1alpha1.DRBDConfig{
										NodeId: &nodeID,
									},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrList[i], scheme)).To(Succeed())
					}
					rvrList[rvrWithoutNodeIDIndex] = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-seq-6",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-6",
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrList[rvrWithoutNodeIDIndex], scheme)).To(Succeed())
				})

				It("assigns valid unique nodeID", func(ctx SpecContext) {
					By("Reconciling until replica gets valid nodeID")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolumeReplica {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[rvrWithoutNodeIDIndex]), rvrList[rvrWithoutNodeIDIndex])).To(Succeed(), "should get updated RVR")
						return rvrList[rvrWithoutNodeIDIndex]
					}).Should(And(
						HaveField("Status.DRBD.Config.NodeId", PointTo(And(
							BeNumerically(">=", v1alpha1.RVRMinNodeID),
							BeNumerically("<=", v1alpha1.RVRMaxNodeID),
						))),
					), "should assign valid nodeID")
				})
			})

			When("isolating nodeIDs by volume", func() {
				BeforeEach(func() {
					nodeID1 := v1alpha1.RVRMinNodeID
					nodeID2 := v1alpha1.RVRMinNodeID + 1
					rvr1 := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-vol1-1",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-1",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID1},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr1, scheme)).To(Succeed())
					rvr2 := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-vol1-2",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-2",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID2},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr2, scheme)).To(Succeed())
					rvrList = []*v1alpha1.ReplicatedVolumeReplica{rvr1, rvr2}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, otherRV)).To(Succeed(), "should create RV for volume-2")
					Expect(cl.Create(ctx, otherRVR)).To(Succeed(), "should create RVR for volume-2")
				})

				It("isolates nodeIDs by volume", func(ctx SpecContext) {
					By("Reconciling until volume-2 gets nodeID MinNodeID independently")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolumeReplica {
						g.Expect(rec.Reconcile(ctx, RequestFor(otherRV))).ToNot(Requeue(), "should not requeue after successful assignment")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(otherRVR), otherRVR)).To(Succeed(), "should get updated RVR")
						return otherRVR
					}).Should(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", v1alpha1.RVRMinNodeID))), "volume-2 should get nodeID MinNodeID independently of volume-1")
				})
			})

			When("filling gaps in nodeIDs", func() {
				var rvrWithoutNodeID1 *v1alpha1.ReplicatedVolumeReplica
				var rvrWithoutNodeID2 *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					By("Creating RVRs with nodeID 0, 2, 3 (gaps at 1 and 4) and two RVRs without nodeID (should fill gaps)")
					rvr = nil
					nodeID0 := v1alpha1.RVRMinNodeID
					nodeID2 := v1alpha1.RVRMinNodeID + 2
					nodeID3 := v1alpha1.RVRMinNodeID + 3
					rvr1 := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-gap-1",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-1",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID0},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr1, scheme)).To(Succeed())
					rvr2 := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-gap-2",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-2",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID2},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr2, scheme)).To(Succeed())
					rvr3 := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-gap-3",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-3",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID3},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvr3, scheme)).To(Succeed())
					rvrWithoutNodeID1 = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-gap-4",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-4",
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID1, scheme)).To(Succeed())
					rvrWithoutNodeID2 = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-gap-5",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-5",
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID2, scheme)).To(Succeed())
					rvrList = []*v1alpha1.ReplicatedVolumeReplica{rvr1, rvr2, rvr3, rvrWithoutNodeID1, rvrWithoutNodeID2}
				})

				It("fills gaps in nodeIDs and assigns unique nodeIDs", func(ctx SpecContext) {
					By("Reconciling until both RVRs get valid unique nodeIDs")
					Eventually(func(g Gomega) bool {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID1), rvrWithoutNodeID1)).To(Succeed(), "should get updated RVR1")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID2), rvrWithoutNodeID2)).To(Succeed(), "should get updated RVR2")
						return rvrWithoutNodeID1.Status != nil &&
							rvrWithoutNodeID1.Status.DRBD != nil &&
							rvrWithoutNodeID1.Status.DRBD.Config != nil &&
							rvrWithoutNodeID1.Status.DRBD.Config.NodeId != nil &&
							*rvrWithoutNodeID1.Status.DRBD.Config.NodeId >= v1alpha1.RVRMinNodeID &&
							*rvrWithoutNodeID1.Status.DRBD.Config.NodeId <= v1alpha1.RVRMaxNodeID &&
							rvrWithoutNodeID2.Status != nil &&
							rvrWithoutNodeID2.Status.DRBD != nil &&
							rvrWithoutNodeID2.Status.DRBD.Config != nil &&
							rvrWithoutNodeID2.Status.DRBD.Config.NodeId != nil &&
							*rvrWithoutNodeID2.Status.DRBD.Config.NodeId >= v1alpha1.RVRMinNodeID &&
							*rvrWithoutNodeID2.Status.DRBD.Config.NodeId <= v1alpha1.RVRMaxNodeID &&
							*rvrWithoutNodeID1.Status.DRBD.Config.NodeId != *rvrWithoutNodeID2.Status.DRBD.Config.NodeId
					}).Should(BeTrue(), "both RVRs should get unique valid nodeIDs")
				})
			})

			When("nodeID already assigned", func() {
				var testRVR *v1alpha1.ReplicatedVolumeReplica
				var testNodeID uint

				BeforeEach(func() {
					testNodeID = v1alpha1.RVRMinNodeID + 3
					testRVR = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-idemp-1",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-1",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &testNodeID},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, testRVR, scheme)).To(Succeed())
					rvrList = []*v1alpha1.ReplicatedVolumeReplica{testRVR}
				})

				It("does not reassign nodeID if already assigned", func(ctx SpecContext) {
					By("Reconciling and verifying nodeID remains unchanged")
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when nodeID already assigned")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRVR), testRVR)).To(Succeed(), "should get updated RVR")
					Expect(testRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", testNodeID))), "nodeID should remain unchanged (idempotent)")
				})
			})

			When("invalid nodeID", func() {
				var rvrWithInvalidNodeID *v1alpha1.ReplicatedVolumeReplica
				var rvrWithoutNodeID *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					invalidNodeID := v1alpha1.RVRMaxNodeID + 1
					rvrWithInvalidNodeID = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-invalid-1",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-1",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &invalidNodeID},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrWithInvalidNodeID, scheme)).To(Succeed())
					rvrWithoutNodeID = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-invalid-2",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-2",
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrWithoutNodeID, scheme)).To(Succeed())
					rvrList = []*v1alpha1.ReplicatedVolumeReplica{rvrWithInvalidNodeID, rvrWithoutNodeID}
				})

				It("ignores nodeID outside valid range and assigns valid nodeID only to RVR without nodeID", func(ctx SpecContext) {
					invalidNodeID := v1alpha1.RVRMaxNodeID + 1
					By("Reconciling until RVR without nodeID gets valid nodeID")
					Eventually(func(g Gomega) bool {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after successful assignment")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithInvalidNodeID), rvrWithInvalidNodeID)).To(Succeed(), "should get RVR with invalid nodeID")
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID), rvrWithoutNodeID)).To(Succeed(), "should get updated RVR without nodeID")
						// RVR with invalid nodeID should keep its invalid nodeID (it's ignored, not overwritten)
						hasInvalidNodeID := rvrWithInvalidNodeID.Status != nil &&
							rvrWithInvalidNodeID.Status.DRBD != nil &&
							rvrWithInvalidNodeID.Status.DRBD.Config != nil &&
							rvrWithInvalidNodeID.Status.DRBD.Config.NodeId != nil &&
							*rvrWithInvalidNodeID.Status.DRBD.Config.NodeId == invalidNodeID
						// RVR without nodeID should get a valid nodeID
						hasValidNodeID := rvrWithoutNodeID.Status != nil &&
							rvrWithoutNodeID.Status.DRBD != nil &&
							rvrWithoutNodeID.Status.DRBD.Config != nil &&
							rvrWithoutNodeID.Status.DRBD.Config.NodeId != nil &&
							*rvrWithoutNodeID.Status.DRBD.Config.NodeId >= v1alpha1.RVRMinNodeID &&
							*rvrWithoutNodeID.Status.DRBD.Config.NodeId <= v1alpha1.RVRMaxNodeID
						return hasInvalidNodeID && hasValidNodeID
					}).Should(BeTrue(), "RVR with invalid nodeID should keep invalid nodeID (ignored), RVR without nodeID should get valid nodeID")
				})
			})

			When("6 replicas with valid nodeIDs (MinNodeID+1 to MinNodeID+6), leaving nodeID free", func() {
				var rvrWithInvalidNodeID *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					By("Creating 6 RVRs with valid nodeID 1-6 and one RVR with invalid nodeID > MaxNodeID (should be ignored)")
					rvr = nil
					rvrList = make([]*v1alpha1.ReplicatedVolumeReplica, 7)
					for i := 1; i < 7; i++ {
						nodeID := v1alpha1.RVRMinNodeID + uint(i)
						rvrList[i-1] = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("rvr-reset-%d", i+1),
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             fmt.Sprintf("node-%d", i+1),
								Type:                 "Diskful",
							},
							Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Config: &v1alpha1.DRBDConfig{NodeId: &nodeID},
								},
							},
						}
						Expect(controllerutil.SetControllerReference(rv, rvrList[i-1], scheme)).To(Succeed())
					}
					invalidNodeID := v1alpha1.RVRMaxNodeID + 1
					rvrWithInvalidNodeID = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-reset-invalid",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-invalid",
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &invalidNodeID},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrWithInvalidNodeID, scheme)).To(Succeed())
					rvrList[6] = rvrWithInvalidNodeID
				})

				It("ignores invalid nodeID and keeps it unchanged", func(ctx SpecContext) {
					invalidNodeID := v1alpha1.RVRMaxNodeID + 1
					By("Reconciling and verifying invalid nodeID remains unchanged (ignored)")
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when invalid nodeID is ignored")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithInvalidNodeID), rvrWithInvalidNodeID)).To(Succeed(), "should get RVR with invalid nodeID")
					Expect(rvrWithInvalidNodeID).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", invalidNodeID))), "invalid nodeID should remain unchanged (ignored, not reset)")
				})
			})

			When("List fails", func() {
				listError := errors.New("failed to list replicas")
				BeforeEach(func() {
					rvrList = nil
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
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

		When("not enough available nodeIDs", func() {
			var rvrList []*v1alpha1.ReplicatedVolumeReplica
			var rvrNeedingNodeIDList []*v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				By("Creating 5 RVRs with nodeID 0-4 (3 available: 5, 6, 7) and 4 RVRs without nodeID (only 3 will get assigned)")
				rvr = nil
				rvrList = make([]*v1alpha1.ReplicatedVolumeReplica, 5)
				for i := 0; i < 5; i++ {
					nodeID := v1alpha1.RVRMinNodeID + uint(i)
					rvrList[i] = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rvr-with-nodeid-%d", i+1),
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             fmt.Sprintf("node-%d", i+1),
							Type:                 "Diskful",
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Config: &v1alpha1.DRBDConfig{NodeId: &nodeID},
							},
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrList[i], scheme)).To(Succeed())
				}
				rvrNeedingNodeIDList = make([]*v1alpha1.ReplicatedVolumeReplica, 4)
				for i := 0; i < 4; i++ {
					rvrNeedingNodeIDList[i] = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rvr-needing-nodeid-%d", i+1),
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             fmt.Sprintf("node-needing-%d", i+1),
							Type:                 "Diskful",
						},
					}
					Expect(controllerutil.SetControllerReference(rv, rvrNeedingNodeIDList[i], scheme)).To(Succeed())
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				for i := range rvrList {
					Expect(cl.Create(ctx, rvrList[i])).To(Succeed(), "should create RVR with nodeID")
				}
				for i := range rvrNeedingNodeIDList {
					Expect(cl.Create(ctx, rvrNeedingNodeIDList[i])).To(Succeed(), fmt.Sprintf("should create RVR %d without nodeID", i+1))
				}
			})

			It("assigns available nodeIDs and handles remaining after RVRs are removed", func(ctx SpecContext) {
				By("First reconcile: 3 available nodeIDs (5, 6, 7), 4 RVRs need nodeID - only 3 should get assigned, reconcile should fail")
				// Reconcile should fail with error because not enough nodeIDs, but 3 RVRs should get assigned
				_, err := rec.Reconcile(ctx, RequestFor(rv))
				Expect(err).To(HaveOccurred(), "reconcile should fail when not enough nodeIDs available")
				Expect(err.Error()).To(ContainSubstring(rvrstatusconfignodeid.ErrNotEnoughAvailableNodeIDsPrefix), "error should mention insufficient nodeIDs")

				// Verify that 3 RVRs got nodeIDs assigned despite the error
				assignedCount := 0
				for i := 0; i < 4; i++ {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrNeedingNodeIDList[i]), rvrNeedingNodeIDList[i])).To(Succeed())
					if rvrNeedingNodeIDList[i].Status != nil && rvrNeedingNodeIDList[i].Status.DRBD != nil && rvrNeedingNodeIDList[i].Status.DRBD.Config != nil && rvrNeedingNodeIDList[i].Status.DRBD.Config.NodeId != nil {
						assignedCount++
					}
				}
				Expect(assignedCount).To(Equal(3), "exactly 3 RVRs should get nodeIDs assigned before reconcile fails")

				By("Finding RVR that didn't get nodeID")
				var rvrWithoutNodeID *v1alpha1.ReplicatedVolumeReplica
				for i := 0; i < 4; i++ {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrNeedingNodeIDList[i]), rvrNeedingNodeIDList[i])).To(Succeed())
					if rvrNeedingNodeIDList[i].Status == nil || rvrNeedingNodeIDList[i].Status.DRBD == nil || rvrNeedingNodeIDList[i].Status.DRBD.Config == nil || rvrNeedingNodeIDList[i].Status.DRBD.Config.NodeId == nil {
						rvrWithoutNodeID = rvrNeedingNodeIDList[i]
						break
					}
				}
				Expect(rvrWithoutNodeID).ToNot(BeNil(), "one RVR should remain without nodeID")

				By("Deleting one RVR with nodeID to free its nodeID")
				freedNodeID1 := v1alpha1.RVRMinNodeID + 2
				Expect(cl.Delete(ctx, rvrList[2])).To(Succeed(), "should delete RVR successfully")

				By("Second reconcile: one nodeID available (2), should assign to remaining RVR")
				Eventually(func(g Gomega) *v1alpha1.ReplicatedVolumeReplica {
					g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after assignment")
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutNodeID), rvrWithoutNodeID)).To(Succeed())
					return rvrWithoutNodeID
				}).Should(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", freedNodeID1))), "remaining RVR should get freed nodeID")

				By("Verifying all RVRs now have nodeIDs assigned")
				for i := 0; i < 4; i++ {
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrNeedingNodeIDList[i]), rvrNeedingNodeIDList[i])).To(Succeed())
					Expect(rvrNeedingNodeIDList[i].Status.DRBD.Config.NodeId).ToNot(BeNil(), fmt.Sprintf("RVR %d should have nodeID assigned", i+1))
				}
			})
		})

		When("Patch fails with non-NotFound error", func() {
			patchError := errors.New("failed to patch status")
			BeforeEach(func() {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
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
				conflictError = kerrors.NewConflict(
					schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumereplicas"},
					rvr.Name,
					errors.New("resourceVersion conflict: the object has been modified"),
				)
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if rvrObj, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							if subResourceName == "status" && rvrObj.Name == rvr.Name {
								patchAttempts++
								if patchAttempts == 1 {
									return conflictError
								}
							}
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("should return error on 409 Conflict and succeed on retry", func(ctx SpecContext) {
				By("First reconcile: should fail with 409 Conflict")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(conflictError), "should return conflict error on first attempt")

				By("Reconciling until nodeID is assigned after conflict resolved")
				Eventually(func(g Gomega) *v1alpha1.ReplicatedVolumeReplica {
					g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed(), "should get updated RVR")
					return rvr
				}).Should(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", v1alpha1.RVRMinNodeID))), "nodeID should be assigned after retry")
			})
		})

	})
})
