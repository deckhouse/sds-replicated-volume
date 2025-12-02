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

package rvstatusconfigsharedsecret_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
)

var _ = Describe("HasUnsupportedAlgorithmError", func() {
	var rvr *v1alpha3.ReplicatedVolumeReplica

	When("RVR has SharedSecretAlgSelectionError", func() {
		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Errors: &v1alpha3.DRBDErrors{
							SharedSecretAlgSelectionError: &v1alpha3.SharedSecretUnsupportedAlgError{
								UnsupportedAlg: "sha256",
							},
						},
					},
				},
			}
		})

		It("should return true", func() {
			Expect(rvstatusconfigsharedsecret.HasUnsupportedAlgorithmError(rvr)).To(BeTrue(), "should return true when SharedSecretAlgSelectionError is set")
		})
	})

	When("RVR has nil Status", func() {
		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
			}
		})

		It("should return false", func() {
			Expect(rvstatusconfigsharedsecret.HasUnsupportedAlgorithmError(rvr)).To(BeFalse(), "should return false when Status is nil")
		})
	})

	When("RVR has nil Status.DRBD", func() {
		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
			}
		})

		It("should return false", func() {
			Expect(rvstatusconfigsharedsecret.HasUnsupportedAlgorithmError(rvr)).To(BeFalse(), "should return false when DRBD is nil")
		})
	})

	When("RVR has nil Status.DRBD.Errors", func() {
		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{},
				},
			}
		})

		It("should return false", func() {
			Expect(rvstatusconfigsharedsecret.HasUnsupportedAlgorithmError(rvr)).To(BeFalse(), "should return false when Errors is nil")
		})
	})

	When("RVR has nil SharedSecretAlgSelectionError", func() {
		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Errors: &v1alpha3.DRBDErrors{},
					},
				},
			}
		})

		It("should return false", func() {
			Expect(rvstatusconfigsharedsecret.HasUnsupportedAlgorithmError(rvr)).To(BeFalse(), "should return false when SharedSecretAlgSelectionError is nil")
		})
	})
})
