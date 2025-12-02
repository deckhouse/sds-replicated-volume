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

package rvrtiebreakercount

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestRvrTieBreakerCountReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rvr_tie_breaker_count Reconciler Suite")
}

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
	return scheme
}

var _ = Describe("Reconciler.reconcileRecalculate", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = newTestScheme()
		ctx = context.Background()
	})

	// Initial State:
	//   FD "node-a": [Diskful]
	//   FD "node-b": [Diskful]
	//   TB: []
	//   Replication: Availability
	// Violates:
	//   - total replica count must be odd
	// Desired state:
	//   FD "node-a": [Diskful]
	//   FD "node-b": [Diskful, TieBreaker]
	//   TB total: 1
	//   replicas total: 3 (odd)
	It("1. creates one TieBreaker for two Diskful on different FDs", func() {
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv1",
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
		}

		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rsc1",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Replication: "Availability",
				Topology:    "",
			},
		}

		nodeA := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-a",
			},
		}
		nodeB := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-b",
			},
		}

		rvrDF1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-a",
				Type:                 "Diskful",
			},
		}
		rvrDF2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df2",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-b",
				Type:                 "Diskful",
			},
		}

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrDF1, rvrDF2).
			Build()

		rec := &Reconciler{
			cl:  cl,
			rdr: cl,
			sch: scheme,
		}

		err := rec.reconcileRecalculate(ctx, RecalculateRequest{VolumeName: "rv1"})
		Expect(err).NotTo(HaveOccurred())

		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		Expect(cl.List(ctx, rvrList)).To(Succeed())

		tbCount := 0
		for _, rvr := range rvrList.Items {
			if rvr.Spec.ReplicatedVolumeName != "rv1" {
				continue
			}
			if rvr.Spec.Type == "TieBreaker" {
				tbCount++
			}
		}

		Expect(tbCount).To(Equal(1))
	})

	// Initial State:
	//   FD "zone-a/node-a": [Diskful]
	//   FD "zone-b/node-b": [Diskful]
	//   TB: []
	//   Replication: Availability
	//   Topology: TransZonal
	// Violates:
	//   - total replica count must be odd
	// Desired state:
	//   FD "zone-a/node-a": [Diskful]
	//   FD "zone-b/node-b": [Diskful]
	//   FD "zone-b/node-c": [TieBreaker]
	//   TB total: 1
	//   replicas total: 3 (odd)
	It("2. creates one TieBreaker for two Diskful on different FDs with TransZonal topology", func() {
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv1",
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
		}

		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rsc1",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Replication: "Availability",
				Topology:    "TransZonal",
			},
		}

		nodeA := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-a",
				Labels: map[string]string{nodeZoneLabel: "zone-a"},
			},
		}
		nodeB := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-b",
				Labels: map[string]string{nodeZoneLabel: "zone-b"},
			},
		}

		rvrDF1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-a",
				Type:                 "Diskful",
			},
		}
		rvrDF2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df2",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-b",
				Type:                 "Diskful",
			},
		}

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrDF1, rvrDF2).
			Build()

		rec := &Reconciler{
			cl:  cl,
			rdr: cl,
			sch: scheme,
		}

		err := rec.reconcileRecalculate(ctx, RecalculateRequest{VolumeName: "rv1"})
		Expect(err).NotTo(HaveOccurred())

		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		Expect(cl.List(ctx, rvrList)).To(Succeed())

		tbCount := 0
		for _, rvr := range rvrList.Items {
			if rvr.Spec.ReplicatedVolumeName != "rv1" {
				continue
			}
			if rvr.Spec.Type == "TieBreaker" {
				tbCount++
			}
		}

		Expect(tbCount).To(Equal(1))
	})

	//   Note: this initial state is not reachable in a real cluster (it violates documented replication rules: "Data is stored in two copies on different nodes"),
	// but the test verifies that if such a state is ever observed, the controller remains a no-op and does not create a useless TieBreaker.
	// Initial State:
	//   FD "node-a": [Diskful, Diskful]
	//   TB: []
	//   Replication: Availability
	// Violates (cluster-level requirement):
	//   - "one FD failure should not break quorum" cannot be achieved for this layout, because all replicas are in a single FD
	// Desired state (nothing should be changed):
	//   FD "node-a": [Diskful, Diskful]
	//   TB total: 0
	//   replicas total: 2
	It("3. does not create TieBreaker when all Diskful are in the same FD", func() {
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv1",
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
		}

		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rsc1",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Replication: "Availability",
				Topology:    "",
			},
		}

		nodeA := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-a",
			},
		}

		rvrDF1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-a",
				Type:                 "Diskful",
			},
		}
		rvrDF2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df2",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-a",
				Type:                 "Diskful",
			},
		}

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(rv, rsc, nodeA, rvrDF1, rvrDF2).
			Build()

		rec := &Reconciler{
			cl:  cl,
			rdr: cl,
			sch: scheme,
		}

		err := rec.reconcileRecalculate(ctx, RecalculateRequest{VolumeName: "rv1"})
		Expect(err).NotTo(HaveOccurred())

		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		Expect(cl.List(ctx, rvrList)).To(Succeed())

		tbCount := 0
		for _, rvr := range rvrList.Items {
			if rvr.Spec.ReplicatedVolumeName != "rv1" {
				continue
			}
			if rvr.Spec.Type == "TieBreaker" {
				tbCount++
			}
		}

		Expect(tbCount).To(Equal(0))
	})

	// Initial State:
	//   FD "node-a": [Diskful]
	//   FD "node-b": [Diskful]
	//   TB: [TieBreaker, TieBreaker]
	// Violates:
	//   - minimality of TieBreaker count for given FD distribution and odd total replica requirement
	// Desired state:
	//   FD "node-a": [Diskful]
	//   FD "node-b": [Diskful, TieBreaker]
	//   TB total: 1
	//   replicas total: 3 (odd)
	It("4. deletes extra TieBreakers and leaves one", func() {
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv1",
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
		}

		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rsc1",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Replication: "Availability",
				Topology:    "",
			},
		}

		nodeA := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-a",
			},
		}
		nodeB := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-b",
			},
		}

		rvrDF1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-a",
				Type:                 "Diskful",
			},
		}
		rvrDF2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-df2",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				NodeName:             "node-b",
				Type:                 "Diskful",
			},
		}

		rvrTB1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-tb1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				Type:                 "TieBreaker",
			},
		}
		rvrTB2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-tb2",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv1",
				Type:                 "TieBreaker",
			},
		}

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrDF1, rvrDF2, rvrTB1, rvrTB2).
			Build()

		rec := &Reconciler{
			cl:  cl,
			rdr: cl,
			sch: scheme,
		}

		err := rec.reconcileRecalculate(ctx, RecalculateRequest{VolumeName: "rv1"})
		Expect(err).NotTo(HaveOccurred())

		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		Expect(cl.List(ctx, rvrList)).To(Succeed())

		tbCount := 0
		for _, rvr := range rvrList.Items {
			if rvr.Spec.ReplicatedVolumeName != "rv1" {
				continue
			}
			if rvr.Spec.Type == "TieBreaker" {
				tbCount++
			}
		}

		Expect(tbCount).To(Equal(1))
	})
})

var _ = Describe("desiredTieBreakerTotal func test", func() {
	// Initial State:
	//   FD "a": 1
	//   FD "b": 1
	//   TB: 0
	// Violates:
	//   - total replica count must be odd
	// Desired state:
	//   TB total: 1
	//   replicas total: 3 (odd)
	//   one possible layout: FD "a": 2, FD "b": 1
	It("1. returns 1 for two FDs with one replica", func() {
		fd := map[fdKey]int{
			"a": 1,
			"b": 1,
		}
		got := desiredTieBreakerTotal(fd)
		Expect(got).To(Equal(1))
	})
})
