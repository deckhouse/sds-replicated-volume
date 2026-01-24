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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("rspPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rspPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when Generation changes", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when EligibleNodesRevision changes", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when Ready condition changes", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status: metav1.ConditionFalse,
							Reason: "NotReady",
						},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when all unchanged", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsp-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when cast fails (conservative)", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when old is nil (conservative)", func() {
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: nil,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when new is nil (conservative)", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})
})

var _ = Describe("rvPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rvPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when spec.replicatedStorageClassName changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-old",
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-new",
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when status.ConfigurationObservedGeneration changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when ConfigurationReady condition changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionFalse,
							Reason: "NotReady",
						},
					},
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when SatisfyEligibleNodes condition changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionFalse,
							Reason: "NotSatisfied",
						},
					},
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
							Reason: "Satisfied",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when all unchanged", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
							Reason: "Satisfied",
						},
					},
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationObservedGeneration: 1,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
							Reason: "Satisfied",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when cast fails (conservative)", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when old is nil (conservative)", func() {
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: nil,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when new is nil (conservative)", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})

	Describe("GenericFunc", func() {
		It("returns false always", func() {
			predicates := rvPredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.GenericFunc != nil {
					rv := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
					}
					e := event.TypedGenericEvent[client.Object]{Object: rv}
					Expect(fp.GenericFunc(e)).To(BeFalse())
				}
			}
		})
	})
})
