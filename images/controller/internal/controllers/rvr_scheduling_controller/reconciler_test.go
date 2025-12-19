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

package rvr_scheduling_controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
)

var _ = Describe("RvrSchedulingController Reconciler", Ordered, func() {
	var (
		scheme     *runtime.Scheme
		builder    *fake.ClientBuilder
		cl         client.WithWatch
		rec        *rvrschedulingcontroller.Reconciler
		mockServer *httptest.Server
	)

	BeforeEach(func() {
		mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				LVGS []struct{ Name string } `json:"lvgs"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			resp := map[string]any{"lvgs": []map[string]any{}}
			for _, lvg := range req.LVGS {
				resp["lvgs"] = append(resp["lvgs"].([]map[string]any), map[string]any{"name": lvg.Name, "score": 100})
			}
			json.NewEncoder(w).Encode(resp)
		}))
		os.Setenv("SCHEDULER_EXTENDER_URL", mockServer.URL)
		scheme = runtime.NewScheme()
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(snc.AddToScheme(scheme))
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
		utilruntime.Must(v1alpha3.AddToScheme(scheme))
		builder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
	})

	AfterEach(func() {
		os.Unsetenv("SCHEDULER_EXTENDER_URL")
		mockServer.Close()
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
	})

	FContext("Diskful phase", func() {
		var (
			rv      *v1alpha3.ReplicatedVolume
			rsc     *v1alpha1.ReplicatedStorageClass
			nodeA   *corev1.Node
			nodeB   *corev1.Node
			rsp     *v1alpha1.ReplicatedStoragePool
			lvgA    *snc.LVMVolumeGroup
			lvgB    *snc.LVMVolumeGroup
			rvrList []*v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-local",
					Finalizers: []string{v1alpha3.ControllerAppFinalizer},
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-local",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha3.ConditionTypeReady,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-local"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Local",
					Topology:     "Zonal",
					Zones:        []string{"zone-a"},
				},
			}

			nodeA = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			rsp = &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "vg-a"}, {Name: "vg-b"},
					},
				},
			}

			lvgA = &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "vg-a"},
				Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-a"}}},
			}
			lvgB = &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "vg-b"},
				Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-b"}}},
			}

			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-local",
						Type:                 v1alpha3.ReplicaTypeDiskful,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-local",
						Type:                 v1alpha3.ReplicaTypeDiskful,
					},
				},
			}
		})

		JustBeforeEach(func() {
			objects := []runtime.Object{rv, rsc, nodeA, nodeB, rsp, lvgA, lvgB}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
				Build()
			rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
		})

		reconcileAndExpectSuccess := func(ctx context.Context) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())
		}

		reconcileAndExpectError := func(ctx context.Context, errSubstring string) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(errSubstring))
		}

		expectReplicasScheduledOnNodes := func(ctx context.Context, expectedNodes ...string) {
			var assignedNodes []string
			for _, rvr := range rvrList {
				updated := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: rvr.Name}, updated)).To(Succeed())
				assignedNodes = append(assignedNodes, updated.Spec.NodeName)
			}
			Expect(assignedNodes).To(ContainElements(expectedNodes))
		}

		expectScheduledCondition := func(ctx context.Context, rvrName string, expectedStatus metav1.ConditionStatus, expectedReason string) {
			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: rvrName}, updated)).To(Succeed())
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha3.ConditionTypeScheduled)
			Expect(cond).ToNot(BeNil(), "Scheduled condition should exist on RVR %s", rvrName)
			Expect(cond.Status).To(Equal(expectedStatus), "Scheduled condition status mismatch on RVR %s", rvrName)
			Expect(cond.Reason).To(Equal(expectedReason), "Scheduled condition reason mismatch on RVR %s", rvrName)
		}

		expectAllReplicasHaveScheduledConditionTrue := func(ctx context.Context) {
			for _, rvr := range rvrList {
				expectScheduledCondition(ctx, rvr.Name, metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
			}
		}

		Context("Zonal topology", func() {
			It("schedules replicas when all publishOn nodes are in the same zone", func(ctx SpecContext) {
				reconcileAndExpectSuccess(ctx)
				expectReplicasScheduledOnNodes(ctx, "node-a", "node-b")
				expectAllReplicasHaveScheduledConditionTrue(ctx)
			})

			When("not enough replicas for publishOn nodes", func() {
				BeforeEach(func() {
					rvrList = rvrList[:1]
				})

				It("schedules available replicas successfully", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)
					updated := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: rvrList[0].Name}, updated)).To(Succeed())
					Expect(updated.Spec.NodeName).To(BeElementOf("node-a", "node-b"))
					expectScheduledCondition(ctx, rvrList[0].Name, metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
				})
			})

			When("publishOn nodes span multiple zones", func() {
				BeforeEach(func() {
					rsc.Spec.Zones = []string{"zone-a", "zone-b"}
					nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
					rvrList[0].Spec.NodeName = "node-a"
				})

				It("returns error when no candidate nodes in target zone", func(ctx SpecContext) {
					reconcileAndExpectError(ctx, "no candidate nodes")
				})
			})
		})

		Context("TransZonal topology", func() {
			BeforeEach(func() {
				rsc.Spec.Topology = "TransZonal"
				rsc.Spec.Zones = []string{"zone-a", "zone-b"}
			})

			When("publishOn nodes are in the same zone", func() {
				It("schedules replicas in available zone", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)
					expectReplicasScheduledOnNodes(ctx, "node-a", "node-b")
					expectAllReplicasHaveScheduledConditionTrue(ctx)
				})
			})

			When("publishOn nodes are in different zones", func() {
				BeforeEach(func() {
					nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
				})

				It("schedules replicas successfully", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)
					expectReplicasScheduledOnNodes(ctx, "node-a", "node-b")
					expectAllReplicasHaveScheduledConditionTrue(ctx)
				})
			})
		})

		Context("Ignored topology", func() {
			BeforeEach(func() {
				rsc.Spec.Topology = "Ignored"
				rsc.Spec.Zones = nil
				nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
			})

			It("schedules replicas on any publishOn nodes", func(ctx SpecContext) {
				reconcileAndExpectSuccess(ctx)
				expectReplicasScheduledOnNodes(ctx, "node-a", "node-b")
				expectAllReplicasHaveScheduledConditionTrue(ctx)
			})
		})

		Context("PublishOn preference", func() {
			var nodeC *corev1.Node
			var nodeD *corev1.Node
			var lvgC *snc.LVMVolumeGroup
			var lvgD *snc.LVMVolumeGroup

			BeforeEach(func() {
				// Setup: 4 nodes (a, b, c, d) in same zone, but only node-a and node-b are in publishOn
				rsc.Spec.Topology = "Ignored"
				rsc.Spec.Zones = nil

				nodeC = &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-c",
						Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
					},
				}
				nodeD = &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-d",
						Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
					},
				}

				// Add LVGs for new nodes
				lvgC = &snc.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{Name: "vg-c"},
					Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-c"}}},
				}
				lvgD = &snc.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{Name: "vg-d"},
					Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-d"}}},
				}

				// Update storage pool to include all nodes
				rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "vg-a"}, {Name: "vg-b"}, {Name: "vg-c"}, {Name: "vg-d"},
				}
			})

			JustBeforeEach(func() {
				objects := []runtime.Object{rv, rsc, nodeA, nodeB, nodeC, nodeD, rsp, lvgA, lvgB, lvgC, lvgD}
				for _, rvr := range rvrList {
					objects = append(objects, rvr)
				}
				cl = fake.NewClientBuilder().
					WithScheme(scheme).
					WithRuntimeObjects(objects...).
					WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
					Build()
				rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
			})

			When("more candidate nodes than replicas", func() {
				BeforeEach(func() {
					// publishOn is [node-a, node-b], but we have 4 candidate nodes
					// and only 1 replica to schedule
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
					}
				})

				It("prefers publishOn nodes over other nodes", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)

					updated := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated)).To(Succeed())
					// Should be scheduled on node-a or node-b (publishOn nodes), not node-c or node-d
					Expect(updated.Spec.NodeName).To(BeElementOf("node-a", "node-b"))
					expectScheduledCondition(ctx, "rvr-1", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
				})
			})

			When("two replicas and two publishOn nodes among four candidates", func() {
				BeforeEach(func() {
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
					}
				})

				It("schedules both replicas on publishOn nodes", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)

					updated1 := &v1alpha3.ReplicatedVolumeReplica{}
					updated2 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated1)).To(Succeed())
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-2"}, updated2)).To(Succeed())

					assignedNodes := []string{updated1.Spec.NodeName, updated2.Spec.NodeName}
					// Both should be on publishOn nodes
					Expect(assignedNodes).To(ContainElements("node-a", "node-b"))
					expectScheduledCondition(ctx, "rvr-1", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
					expectScheduledCondition(ctx, "rvr-2", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
				})
			})

			When("more replicas than publishOn nodes", func() {
				BeforeEach(func() {
					// 3 replicas, but only 2 publishOn nodes
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-3"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
					}
				})

				It("schedules publishOn nodes first, then other nodes", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)

					updated1 := &v1alpha3.ReplicatedVolumeReplica{}
					updated2 := &v1alpha3.ReplicatedVolumeReplica{}
					updated3 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated1)).To(Succeed())
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-2"}, updated2)).To(Succeed())
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updated3)).To(Succeed())

					assignedNodes := []string{updated1.Spec.NodeName, updated2.Spec.NodeName, updated3.Spec.NodeName}
					// publishOn nodes (node-a, node-b) should be used
					Expect(assignedNodes).To(ContainElements("node-a", "node-b"))
					// Third replica goes to node-c or node-d
					Expect(assignedNodes).To(ContainElement(BeElementOf("node-c", "node-d")))
					// All replicas should have Scheduled=True condition
					expectScheduledCondition(ctx, "rvr-1", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
					expectScheduledCondition(ctx, "rvr-2", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
					expectScheduledCondition(ctx, "rvr-3", metav1.ConditionTrue, v1alpha3.ReasonSchedulingReplicaScheduled)
				})
			})

			When("no publishOn nodes specified", func() {
				BeforeEach(func() {
					rv.Spec.PublishOn = nil
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-local",
								Type:                 v1alpha3.ReplicaTypeDiskful,
							},
						},
					}
				})

				It("schedules on any available node", func(ctx SpecContext) {
					reconcileAndExpectSuccess(ctx)

					updated := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated)).To(Succeed())
					Expect(updated.Spec.NodeName).To(BeElementOf("node-a", "node-b", "node-c", "node-d"))
				})
			})
		})
	})

	Context("Diskful phase (non-Local)", func() {
		var (
			rv      *v1alpha3.ReplicatedVolume
			rsc     *v1alpha1.ReplicatedStorageClass
			nodeA   *corev1.Node
			nodeB   *corev1.Node
			nodeC   *corev1.Node
			rsp     *v1alpha1.ReplicatedStoragePool
			lvgB    *snc.LVMVolumeGroup
			rvrList []*v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-diskful",
					Finalizers: []string{v1alpha3.ControllerAppFinalizer},
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-diskful",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha3.ConditionTypeReady,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-diskful"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			nodeA = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeC = nil
			rsp = nil
			lvgB = nil

			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-diskful",
						Type:                 v1alpha3.ReplicaTypeDiskful,
					},
				},
			}
		})

		JustBeforeEach(func() {
			objects := []runtime.Object{rv, rsc, nodeA, nodeB}
			if nodeC != nil {
				objects = append(objects, nodeC)
			}
			if rsp != nil {
				objects = append(objects, rsp)
			}
			if lvgB != nil {
				objects = append(objects, lvgB)
			}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()
			rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
		})

		Context("Any topology with storage pool constraint", func() {
			BeforeEach(func() {
				rsc.Spec.StoragePool = "pool-any"
				rsp = &v1alpha1.ReplicatedStoragePool{
					ObjectMeta: metav1.ObjectMeta{Name: "pool-any"},
					Spec: v1alpha1.ReplicatedStoragePoolSpec{
						Type:            "LVM",
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-b"}},
					},
				}
				lvgB = &snc.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{Name: "vg-b"},
					Spec:       snc.LVMVolumeGroupSpec{Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-b"}},
					Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-b"}}},
				}
			})

			It("schedules replica on node allowed by storage pool", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated)).To(Succeed())
				Expect(updated.Spec.NodeName).To(Equal("node-b"))
			})
		})

		Context("Zonal topology", func() {
			BeforeEach(func() {
				rsc.Spec.Topology = "Zonal"
				rsc.Spec.Zones = []string{"zone-a", "zone-b"}
				rv.Spec.PublishOn = []string{"node-a", "node-b", "node-c"}
				nodeC = &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-c",
						Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
					},
				}
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-existing"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-diskful",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-diskful",
							Type:                 v1alpha3.ReplicaTypeDiskful,
						},
					},
				}
			})

			It("keeps replicas in the same zone as existing ones", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated)).To(Succeed())
				Expect(updated.Spec.NodeName).To(Equal("node-b"))
			})
		})

		Context("TransZonal topology", func() {
			BeforeEach(func() {
				rsc.Spec.Topology = "TransZonal"
				rsc.Spec.Zones = []string{"zone-a", "zone-b", "zone-c"}
				rv.Spec.PublishOn = []string{"node-a", "node-b", "node-c"}
				nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
				nodeC = &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-c",
						Labels: map[string]string{"topology.kubernetes.io/zone": "zone-c"},
					},
				}
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-existing"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-diskful",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-diskful",
							Type:                 v1alpha3.ReplicaTypeDiskful,
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-diskful",
							Type:                 v1alpha3.ReplicaTypeDiskful,
						},
					},
				}
			})

			It("distributes replicas across different zones", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated1 := &v1alpha3.ReplicatedVolumeReplica{}
				updated2 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updated1)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-2"}, updated2)).To(Succeed())

				nodeNames := []string{updated1.Spec.NodeName, updated2.Spec.NodeName}
				Expect(nodeNames).To(ContainElements("node-b", "node-c"))
			})
		})
	})

	Context("Access phase", func() {
		var (
			rv                    *v1alpha3.ReplicatedVolume
			rsc                   *v1alpha1.ReplicatedStorageClass
			nodeA                 *corev1.Node
			nodeB                 *corev1.Node
			rvrList               []*v1alpha3.ReplicatedVolumeReplica
			withStatusSubresource bool
		)

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-access",
					Finalizers: []string{v1alpha3.ControllerAppFinalizer},
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-access",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha3.ConditionTypeReady,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-access"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			nodeA = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			rvrList = nil
			withStatusSubresource = false
		})

		JustBeforeEach(func() {
			objects := []runtime.Object{rv, rsc, nodeA}
			if nodeB != nil {
				objects = append(objects, nodeB)
			}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...)
			if withStatusSubresource {
				builder = builder.WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
			}
			cl = builder.Build()
			rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
		})

		When("one publishOn node has diskful replica", func() {
			BeforeEach(func() {
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 "Access",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-2"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 "Access",
						},
					},
				}
			})

			It("schedules access replica only on free publishOn node", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-1"}, updated1)).To(Succeed())
				updated2 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-2"}, updated2)).To(Succeed())

				nodeNames := []string{updated1.Spec.NodeName, updated2.Spec.NodeName}
				Expect(nodeNames).To(ContainElement("node-b"))
				Expect(nodeNames).To(ContainElement(""))
			})
		})

		When("all publishOn nodes already have replicas", func() {
			BeforeEach(func() {
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-b"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 "Access",
							NodeName:             "node-b",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-unscheduled"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 "Access",
						},
					},
				}
			})

			It("does not schedule unscheduled access replica", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-unscheduled"}, updated)).To(Succeed())
				Expect(updated.Spec.NodeName).To(Equal(""))
			})
		})

		When("checking Scheduled condition", func() {
			BeforeEach(func() {
				rv.Spec.PublishOn = []string{"node-a"}
				nodeB = nil
				withStatusSubresource = true
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-scheduled"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-unscheduled"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-access",
							Type:                 v1alpha3.ReplicaTypeDiskful,
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
					},
				}
			})

			It("sets Scheduled=True for scheduled and Scheduled=False for unscheduled", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updatedScheduled := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-scheduled"}, updatedScheduled)).To(Succeed())
				condScheduled := meta.FindStatusCondition(updatedScheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
				Expect(condScheduled).ToNot(BeNil())
				Expect(condScheduled.Status).To(Equal(metav1.ConditionTrue))
				Expect(condScheduled.Reason).To(Equal(v1alpha3.ReasonSchedulingReplicaScheduled))

				updatedUnscheduled := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-unscheduled"}, updatedUnscheduled)).To(Succeed())
				condUnscheduled := meta.FindStatusCondition(updatedUnscheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
				Expect(condUnscheduled).ToNot(BeNil())
				Expect(condUnscheduled.Status).To(Equal(metav1.ConditionFalse))
				Expect(condUnscheduled.Reason).To(Equal(v1alpha3.ReasonSchedulingWaitingForAnotherReplica))
			})
		})
	})

	Context("TieBreaker phase", func() {
		var (
			rv      *v1alpha3.ReplicatedVolume
			rsc     *v1alpha1.ReplicatedStorageClass
			nodeA   *corev1.Node
			nodeB   *corev1.Node
			nodeC   *corev1.Node
			rvrList []*v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-tb",
					Finalizers: []string{v1alpha3.ControllerAppFinalizer},
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-tb",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha3.ConditionTypeReady,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-tb"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Zonal",
					Zones:        []string{"zone-a"},
				},
			}

			nodeA = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeC = nil

			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-1"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-tb",
						Type:                 "TieBreaker",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-2"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-tb",
						Type:                 "TieBreaker",
					},
				},
			}
		})

		JustBeforeEach(func() {
			objects := []runtime.Object{rv, rsc, nodeA, nodeB}
			if nodeC != nil {
				objects = append(objects, nodeC)
			}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()
			rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
		})

		Context("Zonal topology", func() {
			It("keeps all tiebreakers in the same zone", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated1 := &v1alpha3.ReplicatedVolumeReplica{}
				updated2 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-1"}, updated1)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-2"}, updated2)).To(Succeed())

				Expect(updated1.Spec.NodeName).ToNot(Equal(""))
				Expect(updated2.Spec.NodeName).ToNot(Equal(""))
			})

			When("existing replicas are in multiple zones", func() {
				BeforeEach(func() {
					rv.Spec.PublishOn = nil
					rsc.Spec.Zones = []string{"zone-a", "zone-b"}
					nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 v1alpha3.ReplicaTypeDiskful,
								NodeName:             "node-a",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-b"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 v1alpha3.ReplicaTypeDiskful,
								NodeName:             "node-b",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 "TieBreaker",
							},
						},
					}
				})

				It("returns error", func(ctx SpecContext) {
					_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("cannot satisfy Zonal topology: replicas already exist in multiple zones"))
				})
			})
		})

		Context("TransZonal topology", func() {
			BeforeEach(func() {
				rsc.Spec.Topology = "TransZonal"
				rsc.Spec.Zones = []string{"zone-a", "zone-b", "zone-c"}
				rv.Spec.PublishOn = []string{"node-a", "node-b", "node-c"}
				nodeB.Labels["topology.kubernetes.io/zone"] = "zone-b"
				nodeC = &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-c",
						Labels: map[string]string{"topology.kubernetes.io/zone": "zone-c"},
					},
				}
				rvrList = []*v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-existing"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-tb",
							Type:                 v1alpha3.ReplicaTypeDiskful,
							NodeName:             "node-a",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-tb",
							Type:                 "TieBreaker",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-2"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-tb",
							Type:                 "TieBreaker",
						},
					},
				}
			})

			It("distributes tiebreakers across different zones", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
				Expect(err).ToNot(HaveOccurred())

				updated1 := &v1alpha3.ReplicatedVolumeReplica{}
				updated2 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-1"}, updated1)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-2"}, updated2)).To(Succeed())

				Expect(updated1.Spec.NodeName).ToNot(Equal(""))
				Expect(updated2.Spec.NodeName).ToNot(Equal(""))
				Expect(updated1.Spec.NodeName).ToNot(Equal(updated2.Spec.NodeName))
			})

			When("no free nodes in minimal replica zones", func() {
				BeforeEach(func() {
					rv.Spec.PublishOn = nil
					rsc.Spec.Zones = []string{"zone-a", "zone-b"}
					nodeC = nil
					rvrList = []*v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 v1alpha3.ReplicaTypeDiskful,
								NodeName:             "node-a",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-b"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 v1alpha3.ReplicaTypeDiskful,
								NodeName:             "node-b",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv-tb",
								Type:                 "TieBreaker",
							},
						},
					}
				})

				It("returns error", func(ctx SpecContext) {
					_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("cannot schedule TieBreaker: no free node in zones with minimal replica count"))
				})
			})
		})
	})
})
