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

package rvcontroller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	return scheme
}

var _ = Describe("mapRSCToRVs", func() {
	It("returns nil for non-RSC object", func() {
		cl := fake.NewClientBuilder().
			WithScheme(newScheme()).
			Build()

		mapFn := mapRSCToRVs(cl)

		// Pass a non-RSC object.
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
		result := mapFn(context.Background(), rv)
		Expect(result).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().
			WithScheme(newScheme()).
			Build()

		mapFn := mapRSCToRVs(cl)
		result := mapFn(context.Background(), nil)
		Expect(result).To(BeNil())
	})

	It("returns requests for all RVs referencing the RSC", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
		}

		rv1 := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		rv2 := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-2"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		rv3 := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-3"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-other",
			},
		}

		cl := testhelpers.WithRVByReplicatedStorageClassNameIndex(
			fake.NewClientBuilder().WithScheme(newScheme()),
		).
			WithObjects(rsc, rv1, rv2, rv3).
			Build()

		mapFn := mapRSCToRVs(cl)
		result := mapFn(context.Background(), rsc)

		Expect(result).To(HaveLen(2))
		names := []string{result[0].Name, result[1].Name}
		Expect(names).To(ContainElements("rv-1", "rv-2"))
	})

	It("returns empty slice when no RVs reference the RSC", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-unused"},
		}

		rv1 := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-other",
			},
		}

		cl := testhelpers.WithRVByReplicatedStorageClassNameIndex(
			fake.NewClientBuilder().WithScheme(newScheme()),
		).
			WithObjects(rsc, rv1).
			Build()

		mapFn := mapRSCToRVs(cl)
		result := mapFn(context.Background(), rsc)

		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("mapRVAToRV", func() {
	It("returns nil for non-RVA object", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
		result := mapRVAToRV(context.Background(), rv)
		Expect(result).To(BeNil())
	})

	It("returns nil for nil object", func() {
		result := mapRVAToRV(context.Background(), nil)
		Expect(result).To(BeNil())
	})

	It("returns nil when ReplicatedVolumeName is empty", func() {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "",
				NodeName:             "node-1",
			},
		}
		result := mapRVAToRV(context.Background(), rva)
		Expect(result).To(BeNil())
	})

	It("returns request for the parent RV", func() {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}
		result := mapRVAToRV(context.Background(), rva)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("rv-1"))
	})
})

var _ = Describe("mapRVRToRV", func() {
	It("returns nil for non-RVR object", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
		result := mapRVRToRV(context.Background(), rv)
		Expect(result).To(BeNil())
	})

	It("returns nil for nil object", func() {
		result := mapRVRToRV(context.Background(), nil)
		Expect(result).To(BeNil())
	})

	It("returns nil when ReplicatedVolumeName is empty", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "",
			},
		}
		result := mapRVRToRV(context.Background(), rvr)
		Expect(result).To(BeNil())
	})

	It("returns request for the parent RV", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}
		result := mapRVRToRV(context.Background(), rvr)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("rv-1"))
	})
})
