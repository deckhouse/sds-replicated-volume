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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("RVRPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := RVRPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.Funcs); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
			Expect(preds).NotTo(BeEmpty())
		})

		It("returns true when Generation changes", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 2},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when SatisfyEligibleNodes condition is False", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
							Status: metav1.ConditionFalse,
							Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
						},
					},
				},
			}
			newRVR := oldRVR.DeepCopy()
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when Generation unchanged and SatisfyEligibleNodes is True", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
						},
					},
				},
			}
			newRVR := oldRVR.DeepCopy()
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when Generation unchanged and no SatisfyEligibleNodes condition", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := oldRVR.DeepCopy()
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when only status changes (no gen bump, condition satisfied)", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
						},
					},
				},
			}
			newRVR := oldRVR.DeepCopy()
			// Change some other status field — predicate should still be false.
			newRVR.Status.DatameshRevision = 42
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when type assertion fails (conservative)", func() {
			oldObj := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			newObj := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldObj,
				ObjectNew: newObj,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when old is nil (conservative)", func() {
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: nil,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when new is nil (conservative)", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})

	Describe("DeleteFunc", func() {
		It("returns false always", func() {
			predicates := RVRPredicates()
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			}
			for _, p := range predicates {
				if fp, ok := p.(predicate.Funcs); ok && fp.DeleteFunc != nil {
					e := event.TypedDeleteEvent[client.Object]{Object: rvr}
					Expect(fp.DeleteFunc(e)).To(BeFalse())
				}
			}
		})
	})
})
