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
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
)

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

	When("objects created", func() {
		var (
			rv       v1alpha3.ReplicatedVolume
			rvrList  v1alpha3.ReplicatedVolumeReplicaList
			nodeList []corev1.Node
			rsc      v1alpha1.ReplicatedStorageClass
		)

		BeforeEach(func() {
			rv = v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv1",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc1",
				},
			}

			rsc = v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rsc1",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: "Availability",
					Topology:    "",
				},
			}

			nodeList = []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
			}

			rvrList = v1alpha3.ReplicatedVolumeReplicaList{
				Items: []v1alpha3.ReplicatedVolumeReplica{
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
							ReplicatedVolumeName: rv.Name,
							NodeName:             nodeList[1].Name,
							Type:                 "Diskful",
						},
					},
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, &rv)).To(Succeed())
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

		When("too many tie breakers", func() {
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
			})
		})
	})
})

var _ = Describe("DesiredTieBreakerTotal", func() {
	DescribeTable("returns correct TieBreaker count for fdCount < 4",
		func(fd map[string]int, expected int) {
			got, err := rvrtiebreakercount.DesiredTieBreakerTotal(fd)
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

	DescribeTable("returns error for fdCount >= 4",
		func(fd map[string]int) {
			_, err := rvrtiebreakercount.DesiredTieBreakerTotal(fd)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only supported for less than 4 failure domains"))
		},
		func(fd map[string]int) string {
			return fmt.Sprintf("%d FDs -> error", len(fd))
		},
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 1, "d": 1}),
		Entry(nil, map[string]int{"a": 2, "b": 2, "c": 2, "d": 2}),
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 1, "d": 2}),
		Entry(nil, map[string]int{"a": 1, "b": 1, "c": 1, "d": 1, "e": 1}),
	)
})
