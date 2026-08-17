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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// newDataPlaneTestRV builds a minimal, formed RV at FTT=0, GMDR=0 with a fresh
// effective layout so it reports Ready/Resilient by default; entries mutate it
// toward the specific precedence rule under test.
func newDataPlaneTestRV() *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 1,
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate:              0,
				GuaranteedMinimumDataRedundancy: 0,
			},
			EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
				FailuresToTolerate:              ptr.To(int8(0)),
				GuaranteedMinimumDataRedundancy: ptr.To(int8(0)),
			},
		},
	}
}

var _ = Describe("data-plane conditions", func() {
	Describe("computeReadyReport", func() {
		DescribeTable("follows the precedence order",
			func(mutate func(*v1alpha1.ReplicatedVolume), wantStatus metav1.ConditionStatus, wantReason string) {
				rv := newDataPlaneTestRV()
				mutate(rv)
				report := computeReadyReport(rv)
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},
			Entry("deletion timestamp set",
				func(rv *v1alpha1.ReplicatedVolume) { rv.DeletionTimestamp = ptr.To(metav1.Now()) },
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonTerminating),
			Entry("formation in progress",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.DatameshRevision = 0 },
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonForming),
			Entry("effective FTT nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.EffectiveLayout.FailuresToTolerate = nil },
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown),
			Entry("FTT < 0",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.FailuresToTolerate = ptr.To(int8(-1))
				},
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost),
			Entry("FTT >= 0 but GMDR < 0",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.GuaranteedMinimumDataRedundancy = ptr.To(int8(-1))
				},
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonInsufficientUpToDateReplicas),
			Entry("FTT >= 0 and GMDR >= 0",
				func(*v1alpha1.ReplicatedVolume) {},
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),
		)
	})

	Describe("computeResilientReport", func() {
		DescribeTable("computes the Resilient report",
			func(mutate func(*v1alpha1.ReplicatedVolume), wantPresent bool, wantStatus metav1.ConditionStatus, wantReason string) {
				rv := newDataPlaneTestRV()
				mutate(rv)
				report := computeResilientReport(rv)
				Expect(report.present).To(Equal(wantPresent))
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},
			Entry("deletion timestamp set",
				func(rv *v1alpha1.ReplicatedVolume) { rv.DeletionTimestamp = ptr.To(metav1.Now()) },
				false, metav1.ConditionStatus(""), ""),
			Entry("configuration nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.Configuration = nil },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonForming),
			Entry("formation in progress",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.DatameshRevision = 0 },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonForming),
			Entry("effective FTT nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.EffectiveLayout.FailuresToTolerate = nil },
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondResilientReasonStatusUnknown),
			Entry("effective meets intent",
				func(*v1alpha1.ReplicatedVolume) {},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondResilientReasonResilient),
			Entry("effective below intent",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.Configuration.FailuresToTolerate = 1 },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonDegraded),
		)
	})
})
