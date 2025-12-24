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

package rvrtiebreakercount_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
)

var errExpectedTestError = errors.New("test error")

var _ = Describe("Reconcile", func() {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	var (
		builder *fake.ClientBuilder
		cl      client.WithWatch
		rec     *rvrtiebreakercount.Reconciler
	)

	BeforeEach(func() {
		builder = fake.NewClientBuilder().WithScheme(scheme)
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec, _ = rvrtiebreakercount.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
	})

	It("returns nil when ReplicatedVolume not found", func(ctx SpecContext) {
		result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	When("rv created", func() {
		var rv v1alpha1.ReplicatedVolume
		BeforeEach(func() {
			rv = v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc1",
				},
			}

			setRVInitializedCondition(&rv, metav1.ConditionTrue)
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, &rv)).To(Succeed())
		})

		When("ReplicatedStorageClassName is empty", func() {
			BeforeEach(func() {
				rv.Spec.ReplicatedStorageClassName = ""
			})

			It("returns nil when ReplicatedStorageClassName is empty", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).Error().NotTo(HaveOccurred())
			})
		})

		When("RVRs created", func() {
			var (
				rvrList  v1alpha1.ReplicatedVolumeReplicaList
				nodeList []corev1.Node
				rsc      v1alpha1.ReplicatedStorageClass
			)

			BeforeEach(func() {
				rsc = v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication: "Availability",
						Topology:    "",
					},
				}

				// reset lists before populating them
				nodeList = nil
				rvrList = v1alpha1.ReplicatedVolumeReplicaList{}

				for i := 1; i <= 2; i++ {
					node := corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("node-%d", i),
						},
					}
					nodeList = append(nodeList, node)

					rvrList.Items = append(rvrList.Items, v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rvr-df%d", i),
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             node.Name,
							Type:                 v1alpha1.ReplicaTypeDiskful,
						},
					})
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, &rsc)).To(Succeed())
				for i := range nodeList {
					Expect(cl.Create(ctx, &nodeList[i])).To(Succeed())
				}
				for i := range rvrList.Items {
					Expect(cl.Create(ctx, &rvrList.Items[i])).To(Succeed())
				}
			})

			When("RV is not scheduled yet", func() {
				BeforeEach(func() {
					setRVInitializedCondition(&rv, metav1.ConditionFalse)
				})

				It("skips reconciliation until Scheduled=True", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					Expect(cl.List(ctx, &rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(0)))
				})
			})

			// Initial State:
			//   FD "node-1": [Diskful]
			//   FD "node-2": [Diskful]
			//   TB: []
			//   Replication: Availability
			// Violates:
			//   - total replica count must be odd
			// Desired state:
			//   FD "node-1": [Diskful]
			//   FD "node-2": [Diskful, TieBreaker]
			//   TB total: 1
			//   replicas total: 3 (odd)
			It("1. creates one TieBreaker for two Diskful on different FDs", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				Expect(cl.List(ctx, &rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))

			})

			When("Access replicas", func() {
				BeforeEach(func() {
					rv = v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "rv1",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Spec: v1alpha1.ReplicatedVolumeSpec{
							ReplicatedStorageClassName: "rsc1",
						},
					}
					setRVInitializedCondition(&rv, metav1.ConditionTrue)
					rsc = v1alpha1.ReplicatedStorageClass{
						ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
						Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: "Availability"},
					}
					nodeList = []corev1.Node{
						{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
						{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
					}
					rvrList.Items = []v1alpha1.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-df1"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-1",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-acc1"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-2",
								Type:                 v1alpha1.ReplicaTypeAccess,
							},
						},
					}
				})

				It("counts Access replicas in FD distribution", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))
				})
			})

			/*

			 */
			When("more than one TieBreaker is required", func() {
				BeforeEach(func() {
					rv = v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "rv1",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Spec: v1alpha1.ReplicatedVolumeSpec{
							ReplicatedStorageClassName: "rsc1",
						},
					}
					setRVInitializedCondition(&rv, metav1.ConditionTrue)
					rsc = v1alpha1.ReplicatedStorageClass{
						ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
						Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: "Availability"},
					}
					nodeList = []corev1.Node{
						{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
						{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
						{ObjectMeta: metav1.ObjectMeta{Name: "node-c"}},
					}
					rvrList.Items = []v1alpha1.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-df-a1"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-a",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-df-b1"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-b",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-df-c1"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-c",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-acc-c2"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-c",
								Type:                 v1alpha1.ReplicaTypeAccess,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-acc-c3"},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-c",
								Type:                 v1alpha1.ReplicaTypeAccess,
							},
						},
					}
				})

				It("creates two TieBreakers for FD distribution 1+1+3", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(2)))
				})
			})

			When("replicas without NodeName", func() {
				BeforeEach(func() {
					rsc = v1alpha1.ReplicatedStorageClass{
						ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
						Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: "Availability"},
					}
					nodeList = []corev1.Node{
						{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
					}
					rvrList.Items = rvrList.Items[:1]
					rvrList.Items[0] = v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-df1"},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "",
							Type:                 v1alpha1.ReplicaTypeDiskful,
						},
					}
				})

				It("handles replicas without NodeName", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))
				})
			})

			When("different zones", func() {
				BeforeEach(func() {
					rsc.Spec.Topology = "TransZonal"
					rsc.Spec.Zones = []string{"zone-0", "zone-1"}
					for i := range nodeList {
						nodeList[i].Labels = map[string]string{corev1.LabelTopologyZone: fmt.Sprintf("zone-%d", i)}
					}
				})
				// Initial State:
				//   FD "zone-a/node-1": [Diskful]
				//   FD "zone-b/node-2": [Diskful]
				//   TB: []
				//   Replication: Availability
				//   Topology: TransZonal
				// Violates:
				//   - total replica count must be odd
				// Desired state:
				//   FD "zone-a/node-1": [Diskful]
				//   FD "zone-b/node-2": [Diskful]
				//   FD "zone-b/node-3": [TieBreaker]
				//   TB total: 1
				//   replicas total: 3 (odd)
				It("2. creates one TieBreaker for two Diskful on different FDs with TransZonal topology", func(ctx SpecContext) {

					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					Expect(cl.List(ctx, &rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))
				})
			})

			When("replicas on the same node", func() {
				BeforeEach(func() {
					for i := range rvrList.Items {
						rvrList.Items[i].Spec.NodeName = nodeList[0].Name
					}
				})

				It("3. create TieBreaker when all Diskful are in the same FD", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
					Expect(cl.List(ctx, &rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))
				})
			})

			When("extra TieBreakers", func() {
				BeforeEach(func() {
					rvrList.Items = []v1alpha1.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df1",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             nodeList[0].Name,
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df2",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								NodeName:             "node-2",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-tb1",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								Type:                 v1alpha1.ReplicaTypeTieBreaker,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-tb2",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								Type:                 v1alpha1.ReplicaTypeTieBreaker,
							},
						},
					}
				})

				// Initial State:
				//   FD "node-1": [Diskful]
				//   FD "node-2": [Diskful]
				//   TB: [TieBreaker, TieBreaker]
				// Violates:
				//   - minimality of TieBreaker count for given FD distribution and odd total replica requirement
				// Desired state:
				//   FD "node-1": [Diskful]
				//   FD "node-2": [Diskful, TieBreaker]
				//   TB total: 1
				//   replicas total: 3 (odd)
				It("4. deletes extra TieBreakers and leaves one", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					Expect(cl.List(ctx, &rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))
				})

				When("Delete RVR fails", func() {
					BeforeEach(func() {
						builder.WithInterceptorFuncs(interceptor.Funcs{
							Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
								if rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
									return errExpectedTestError
								}
								return c.Delete(ctx, obj, opts...)
							},
						})
					})

					It("returns same error", func(ctx SpecContext) {
						Expect(rec.Reconcile(
							ctx,
							reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)},
						)).Error().To(MatchError(errExpectedTestError))
					})
				})
			})

			DescribeTableSubtree("propagates client errors",
				func(setupInterceptors func(*fake.ClientBuilder)) {
					BeforeEach(func() {
						setupInterceptors(builder)
					})

					It("returns same error", func(ctx SpecContext) {
						Expect(rec.Reconcile(
							ctx,
							reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)},
						)).Error().To(MatchError(errExpectedTestError))
					})
				},
				Entry("Get ReplicatedVolume fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
								return errExpectedTestError
							}
							return c.Get(ctx, key, obj, opts...)
						},
					})
				}),
				Entry("Get ReplicatedStorageClass fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
								return errExpectedTestError
							}
							return c.Get(ctx, key, obj, opts...)
						},
					})
				}),
				Entry("List Nodes fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*corev1.NodeList); ok {
								return errExpectedTestError
							}
							return c.List(ctx, list, opts...)
						},
					})
				}),
				Entry("List ReplicatedVolumeReplicaList fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
								return errExpectedTestError
							}
							return c.List(ctx, list, opts...)
						},
					})
				}),
				Entry("Create RVR fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
							if rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
								return errExpectedTestError
							}
							return c.Create(ctx, obj, opts...)
						},
					})
				}),
			)

		})

	})
})

type FDReplicaCounts struct {
	Diskful    int
	Access     int
	TieBreaker int
}

func setRVInitializedCondition(rv *v1alpha1.ReplicatedVolume, status metav1.ConditionStatus) {
	rv.Status = &v1alpha1.ReplicatedVolumeStatus{
		Conditions: []metav1.Condition{{
			Type:               v1alpha1.ConditionTypeRVInitialized,
			Status:             status,
			LastTransitionTime: metav1.Now(),
			Reason:             "test",
		}},
	}
}

var _ = Describe("DesiredTieBreakerTotal", func() {
	DescribeTableSubtree("returns correct TieBreaker count for fdCount < 4",
		func(_ string, fdExtended map[string]FDReplicaCounts, expected int) {
			When("reconciler creates expected TieBreaker replicas", func() {
				scheme := runtime.NewScheme()
				Expect(corev1.AddToScheme(scheme)).To(Succeed())
				Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

				var (
					builder *fake.ClientBuilder
					cl      client.WithWatch
					rec     *rvrtiebreakercount.Reconciler
					rv      *v1alpha1.ReplicatedVolume
				)

				BeforeEach(func() {

					cl = nil
					rec = nil

					rv = &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "rv1",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Spec: v1alpha1.ReplicatedVolumeSpec{
							ReplicatedStorageClassName: "rsc1",
						},
					}
					setRVInitializedCondition(rv, metav1.ConditionTrue)

					zones := maps.Keys(fdExtended)
					rsc := &v1alpha1.ReplicatedStorageClass{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rsc1",
						},
						Spec: v1alpha1.ReplicatedStorageClassSpec{
							Replication: "Availability",
							Topology:    "TransZonal",
							Zones:       slices.Collect(zones),
						},
					}

					var objects []client.Object
					objects = append(objects, rv, rsc)

					for fdName, fdReplicaCounts := range fdExtended {
						var nodeNameSlice []string
						for i := range 10 {
							nodeName := fmt.Sprintf("node-%s-%d", fdName, i)
							node := &corev1.Node{
								ObjectMeta: metav1.ObjectMeta{
									Name:   nodeName,
									Labels: map[string]string{corev1.LabelTopologyZone: fdName},
								},
							}
							objects = append(objects, node)
							nodeNameSlice = append(nodeNameSlice, nodeName)

						}
						index := 0
						for j := 0; j < fdReplicaCounts.Diskful; j++ {
							rvr := &v1alpha1.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-df-%s-%d", fdName, j+1),
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             nodeNameSlice[index],
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							}
							objects = append(objects, rvr)
							index++
						}

						for j := 0; j < fdReplicaCounts.Access; j++ {
							rvr := &v1alpha1.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-ac-%s-%d", fdName, j+1),
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             nodeNameSlice[index],
									Type:                 v1alpha1.ReplicaTypeAccess,
								},
							}
							objects = append(objects, rvr)
							index++
						}

						for j := 0; j < fdReplicaCounts.TieBreaker; j++ {
							rvr := &v1alpha1.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-tb-%s-%d", fdName, j+1),
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             nodeNameSlice[index],
									Type:                 v1alpha1.ReplicaTypeTieBreaker,
								},
							}
							objects = append(objects, rvr)
							index++
						}
					}
					builder = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
				})

				JustBeforeEach(func() {
					cl = builder.Build()
					rec, _ = rvrtiebreakercount.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
				})

				It("Reconcile works", func(ctx SpecContext) {
					req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rv)}
					result, err := rec.Reconcile(context.Background(), req)

					fmt.Fprintf(GinkgoWriter, "  reconcile result: %#v, err: %v\n", result, err)

					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, rvrList)).To(Succeed())

					fmt.Fprintf(GinkgoWriter, "  total replicas after reconcile: %d\n", len(rvrList.Items))

					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(expected)))
				})
			})
		},
		func(name string, fd map[string]FDReplicaCounts, expected int) string {
			// Sort zone names for predictable output
			zones := slices.Collect(maps.Keys(fd))
			slices.Sort(zones)

			s := []string{}
			for _, zone := range zones {
				counts := fd[zone]
				// Sum only Diskful + Access (without TieBreaker)
				total := counts.Diskful + counts.Access
				s = append(s, fmt.Sprintf("%d", total))
			}
			return fmt.Sprintf("case %s: %d FDs, %s -> %d", name, len(fd), strings.Join(s, "+"), expected)
		},
		Entry(nil, "1", map[string]FDReplicaCounts{}, 0),
		Entry(nil, "2", map[string]FDReplicaCounts{"a": {Diskful: 1}}, 0),
		Entry(nil, "3", map[string]FDReplicaCounts{"a": {Diskful: 0}, "b": {Diskful: 0}}, 0),
		Entry(nil, "4", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}}, 1),
		Entry(nil, "5", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 2}, "c": {}}, 2),
		Entry(nil, "6", map[string]FDReplicaCounts{"a": {Diskful: 2}, "b": {Diskful: 2}, "c": {}}, 1),
		Entry(nil, "7", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 3}, "c": {}}, 3),
		Entry(nil, "8", map[string]FDReplicaCounts{"a": {Diskful: 2}, "b": {Diskful: 3}, "c": {}}, 2),
		Entry(nil, "8.1", map[string]FDReplicaCounts{"a": {Diskful: 2}, "b": {Diskful: 3}}, 0),
		Entry(nil, "9", map[string]FDReplicaCounts{"a": {Diskful: 3}, "b": {Diskful: 3}, "c": {}}, 3),
		Entry(nil, "10", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}, "c": {Diskful: 1}}, 0),

		Entry(nil, "11", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}, "c": {Diskful: 2}}, 1),
		Entry(nil, "12", map[string]FDReplicaCounts{"a": {Diskful: 2}, "b": {Diskful: 2}, "c": {Diskful: 2}}, 1),
		Entry(nil, "13", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 2}, "c": {Diskful: 2}}, 0),
		Entry(nil, "14", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}, "c": {Diskful: 3}}, 2),
		Entry(nil, "15", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 3}, "c": {Diskful: 5}}, 4),
		// Test cases with mixed replica types
		Entry(nil, "16", map[string]FDReplicaCounts{"a": {Diskful: 1, Access: 1}, "b": {Diskful: 1}}, 0),
		Entry(nil, "17", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Access: 1}}, 1),
		Entry(nil, "18", map[string]FDReplicaCounts{"a": {Diskful: 1, Access: 1}, "b": {Diskful: 1, Access: 1}}, 1),
		Entry(nil, "19", map[string]FDReplicaCounts{"a": {Diskful: 2, Access: 1}, "b": {Diskful: 1, Access: 2}}, 1),
		Entry(nil, "20", map[string]FDReplicaCounts{"a": {Diskful: 1, Access: 1}, "b": {Diskful: 1, Access: 1}, "c": {Diskful: 1}}, 0),
		Entry(nil, "21", map[string]FDReplicaCounts{"a": {Diskful: 2, Access: 1, TieBreaker: 1}, "b": {Diskful: 1}, "c": {Diskful: 1}, "d": {}}, 4),
		// with deletion of existing TBs
		Entry(nil, "22", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}, "c": {Diskful: 1}, "d": {TieBreaker: 1}}, 0),
		Entry(nil, "23", map[string]FDReplicaCounts{"a": {Diskful: 1}, "b": {Diskful: 1}, "c": {Diskful: 1}, "d": {TieBreaker: 2}}, 0),
		Entry(nil, "24", map[string]FDReplicaCounts{"a": {Diskful: 1, Access: 1}, "b": {Diskful: 1}, "c": {Diskful: 1}, "d": {TieBreaker: 2}}, 1),
	)
})
