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

package rsccontroller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-common-lib/fsreserve"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// The share of the filesystem reserved for the superuser is an annotation on the
// ReplicatedStorageClass, and a CSI driver only ever sees StorageClass.parameters.
// These specs pin the hand-over between the two.
var _ = Describe("computeTargetStorageClass reserved percent", func() {
	newRSC := func(annotations map[string]string) *v1alpha1.ReplicatedStorageClass {
		return &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Annotations: annotations},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ReclaimPolicy: "Delete",
				VolumeAccess:  v1alpha1.VolumeAccessLocal,
			},
		}
	}

	It("leaves the parameter out when the annotation is absent", func() {
		sc, err := computeTargetStorageClass(newRSC(nil), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Parameters).NotTo(HaveKey(fsreserve.ParameterKey))
	})

	It("leaves the parameter out for an explicit zero", func() {
		sc, err := computeTargetStorageClass(newRSC(map[string]string{fsreserve.AnnotationKey: "0"}), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Parameters).NotTo(HaveKey(fsreserve.ParameterKey))
	})

	It("carries a percentage into the parameters", func() {
		sc, err := computeTargetStorageClass(newRSC(map[string]string{fsreserve.AnnotationKey: "5"}), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Parameters).To(HaveKeyWithValue(fsreserve.ParameterKey, "5"))
	})

	It("fails the reconcile on an out-of-range value instead of every volume", func() {
		_, err := computeTargetStorageClass(newRSC(map[string]string{fsreserve.AnnotationKey: "300"}), nil)
		Expect(err).To(HaveOccurred())
	})

	It("ignores unrelated annotations", func() {
		sc, err := computeTargetStorageClass(newRSC(map[string]string{"example.com/other": "nonsense"}), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sc.Parameters).NotTo(HaveKey(fsreserve.ParameterKey))
	})
})
