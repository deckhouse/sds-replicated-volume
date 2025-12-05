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
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
)

var expectedError = errors.New("test error")

var _ = Describe("Reconcile", func() {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

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
		rec = rvrtiebreakercount.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
	})

	It("returns nil when ReplicatedVolume not found", func(ctx SpecContext) {
		result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	When("rv created", func() {
		var rv v1alpha3.ReplicatedVolume
		BeforeEach(func() {
			rv = v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv1",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc1",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, &rv)).To(Succeed())
		})

		When("ReplicatedStorageClassName is empty", func() {
			BeforeEach(func() {
				rv.Spec.ReplicatedStorageClassName = ""
			})

			It("returns nil when ReplicatedStorageClassName is empty", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).Error().NotTo(HaveOccurred())
			})
		})

		When("RVRs created", func() {
			var (
				rvrList  v1alpha3.ReplicatedVolumeReplicaList
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
				rvrList = v1alpha3.ReplicatedVolumeReplicaList{}

				for i := 1; i <= 2; i++ {
					node := corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("node-%d", i),
						},
					}
					nodeList = append(nodeList, node)

					rvrList.Items = append(rvrList.Items, v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rvr-df%d", i),
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             node.Name,
							Type:                 "Diskful",
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

			When("SetControllerReference fails", func() {
				BeforeEach(func() {
					rsc.Spec.Replication = "Availability"
					rvrList.Items = []v1alpha3.ReplicatedVolumeReplica{{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-df1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "node-1",
							Type:                 "Diskful",
						},
					}, {
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-df2"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "node-2",
							Type:                 "Diskful",
						},
					}}

					old := scheme
					DeferCleanup(func() { scheme = old })
					scheme = nil
				})
				It("returns error when SetControllerReference fails", func(ctx SpecContext) {
					_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).To(HaveOccurred())
				})
			})

			When("Access replicas", func() {
				BeforeEach(func() {
					rv = v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv1"},
						Spec: v1alpha3.ReplicatedVolumeSpec{
							ReplicatedStorageClassName: "rsc1",
						},
					}
					rsc = v1alpha1.ReplicatedStorageClass{
						ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
						Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: "Availability"},
					}
					nodeList = []corev1.Node{
						{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
						{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
					}
					rvrList.Items = []v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-df1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-acc1"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-2",
								Type:                 "Access",
							},
						},
					}
				})

				It("counts Access replicas in FD distribution", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(1)))
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
					rvrList.Items[0] = v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-df1"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "",
							Type:                 "Diskful",
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
					for i := range nodeList {
						nodeList[i].Labels = map[string]string{rvrtiebreakercount.NodeZoneLabel: fmt.Sprintf("zone-%d", i)}
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

				//   Note: this initial state is not reachable in a real cluster (it violates documented replication rules: "Data is stored in two copies on different nodes"),
				// but the test verifies that if such a state is ever observed, the controller remains a no-op and does not create a useless TieBreaker.
				// Initial State:
				//   FD "node-1": [Diskful, Diskful]
				//   TB: []
				//   Replication: Availability
				// Violates (cluster-level requirement):
				//   - "one FD failure should not break quorum" cannot be achieved for this layout, because all replicas are in a single FD
				// Desired state (nothing should be changed):
				//   FD "node-1": [Diskful, Diskful]
				//   TB total: 0
				//   replicas total: 2
				It("3. does not create TieBreaker when all Diskful are in the same FD", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
					Expect(cl.List(ctx, &rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(0)))
				})
			})

			When("extra TieBreakers", func() {
				BeforeEach(func() {
					rvrList.Items = []v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             nodeList[0].Name,
								Type:                 "Diskful",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								NodeName:             "node-2",
								Type:                 "Diskful",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-tb1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								Type:                 "TieBreaker",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-tb2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "rv1",
								Type:                 "TieBreaker",
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
								if rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok && rvr.Spec.Type == "TieBreaker" {
									return expectedError
								}
								return c.Delete(ctx, obj, opts...)
							},
						})
					})

					It("returns same error", func(ctx SpecContext) {
						Expect(rec.Reconcile(
							ctx,
							reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)},
						)).Error().To(MatchError(expectedError))
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
						)).Error().To(MatchError(expectedError))
					})
				},
				Entry("Get ReplicatedVolume fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
								return expectedError
							}
							return c.Get(ctx, key, obj, opts...)
						},
					})
				}),
				Entry("Get ReplicatedStorageClass fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
								return expectedError
							}
							return c.Get(ctx, key, obj, opts...)
						},
					})
				}),
				Entry("List Nodes fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*corev1.NodeList); ok {
								return expectedError
							}
							return c.List(ctx, list, opts...)
						},
					})
				}),
				Entry("List ReplicatedVolumeReplicaList fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
								return expectedError
							}
							return c.List(ctx, list, opts...)
						},
					})
				}),
				Entry("Create RVR fails", func(b *fake.ClientBuilder) {
					b.WithInterceptorFuncs(interceptor.Funcs{
						Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
							if rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok && rvr.Spec.Type == "TieBreaker" {
								return expectedError
							}
							return c.Create(ctx, obj, opts...)
						},
					})
				}),
			)

		})

	})
})

var _ = Describe("DesiredTieBreakerTotal", func() {
	DescribeTable("returns correct TieBreaker count for fdCount < 4",
		func(fd map[string]int, expected int) {
			got, err := rvrtiebreakercount.CalculateDesiredTieBreakerTotal(fd)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(expected))
		},
		func(fd map[string]int, expected int) string {
			s := []string{}
			for _, v := range fd {
				s = append(s, fmt.Sprintf("%d", v))
			}
			return fmt.Sprintf("%d FDs, %s replicas -> %d", len(fd), strings.Join(s, "+"), expected)
		},
		Entry(nil, map[string]int{}, 0),
		Entry(nil, map[string]int{"a": 1}, 0),
		Entry(nil, map[string]int{"a": 0, "b": 0}, 0),
		Entry(nil, map[string]int{"a": 1, "b": 1}, 1),
		Entry(nil, map[string]int{"a": 1, "b": 2}, 0),
		Entry(nil, map[string]int{"a": 2, "b": 2}, 1),
		Entry(nil, map[string]int{"a": 1, "b": 3}, 1),
		Entry(nil, map[string]int{"a": 2, "b": 3}, 0),
		Entry(nil, map[string]int{"a": 3, "b": 3}, 1),
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 1}, 0),
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 2}, 1),
		Entry(nil, map[string]int{"a": 2, "b": 2, "c": 2}, 1),
		Entry(nil, map[string]int{"a": 1, "b": 2, "c": 2}, 0),
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 3}, 2),
	)
})
