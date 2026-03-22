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

package rvrschedulingcontroller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

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
