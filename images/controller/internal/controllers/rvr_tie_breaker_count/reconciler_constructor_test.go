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
	"fmt"

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

type fdLayout struct {
	Name       string
	Diskful    int
	Access     int
	TieBreaker int
}

var _ = Describe("Reconcile from cluster layout", func() {
	It("creates expected number of TieBreakers for 5 zones with mixed Diskful and Access replicas", func(ctx SpecContext) {
		fds := []fdLayout{
			{Name: "zone-1", Diskful: 1, Access: 1},
			{Name: "zone-2", Diskful: 1, Access: 1},
			{Name: "zone-3", Diskful: 1, Access: 0},
			{Name: "zone-4", Diskful: 1, Access: 0},
			{Name: "zone-5", Diskful: 1, Access: 0},
		}
		expectedTieBreakers := 0

		fmt.Fprintln(GinkgoWriter, "scenario: 5 zones with mixed Diskful and Access replicas")
		for _, fd := range fds {
			fmt.Fprintf(GinkgoWriter, "  %s: DF=%d AC=%d\n", fd.Name, fd.Diskful, fd.Access)
		}

		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

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

		var objects []client.Object
		objects = append(objects, rv, rsc)

		for i, fd := range fds {
			nodeName := fmt.Sprintf("node-%d", i+1)

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: map[string]string{rvrtiebreakercount.NodeZoneLabel: fd.Name},
				},
			}
			objects = append(objects, node)

			for j := 0; j < fd.Diskful; j++ {
				rvr := &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("rvr-df-%s-%d", fd.Name, j+1),
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             nodeName,
						Type:                 "Diskful",
					},
				}
				objects = append(objects, rvr)
			}

			for j := 0; j < fd.Access; j++ {
				rvr := &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("rvr-ac-%s-%d", fd.Name, j+1),
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             nodeName,
						Type:                 "Access",
					},
				}
				objects = append(objects, rvr)
			}

			for j := 0; j < fd.TieBreaker; j++ {
				rvr := &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("rvr-ac-%s-%d", fd.Name, j+1),
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             nodeName,
						Type:                 "TieBreaker",
					},
				}
				objects = append(objects, rvr)
			}
		}

		builder := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...)

		cl := builder.Build()
		rec := rvrtiebreakercount.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)

		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rv)}
		result, err := rec.Reconcile(context.Background(), req)

		fmt.Fprintf(GinkgoWriter, "  reconcile result: %#v, err: %v\n", result, err)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		Expect(cl.List(ctx, rvrList)).To(Succeed())

		fmt.Fprintf(GinkgoWriter, "  total replicas after reconcile: %d\n", len(rvrList.Items))

		Expect(rvrList.Items).To(HaveTieBreakerCount(Equal(expectedTieBreakers)))
	})
})
