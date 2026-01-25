/*
Copyright 2026 Flant JSC

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

package rvrcontroller

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestRVRController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rvr_controller Suite")
}

var _ = Describe("mapRVToRVRs", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns requests for RVRs belonging to the RV", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
		}
		rvr2 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
		}
		rvrOther := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-other"},
		}

		cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rv, rvr1, rvr2, rvrOther),
		).Build()

		mapFunc := mapRVToRVRs(cl)
		requests := mapFunc(context.Background(), rv)

		Expect(requests).To(HaveLen(2))
		names := []string{requests[0].Name, requests[1].Name}
		Expect(names).To(ContainElements("rvr-1", "rvr-2"))
	})

	It("returns empty slice when no RVRs belong to the RV", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-unused"},
		}
		rvrOther := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-other"},
		}

		cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rv, rvrOther),
		).Build()

		mapFunc := mapRVToRVRs(cl)
		requests := mapFunc(context.Background(), rv)

		Expect(requests).To(BeEmpty())
	})

	It("returns nil for non-RV object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapRVToRVRs(cl)
		requests := mapFunc(context.Background(), &corev1.Node{})

		Expect(requests).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapRVToRVRs(cl)
		requests := mapFunc(context.Background(), nil)

		Expect(requests).To(BeNil())
	})

	It("returns nil when List fails", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return errors.New("list error")
				},
			}).
			Build()

		mapFunc := mapRVToRVRs(cl)
		requests := mapFunc(context.Background(), rv)

		Expect(requests).To(BeNil())
	})
})
