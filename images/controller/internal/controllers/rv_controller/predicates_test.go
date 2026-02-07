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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

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

		It("returns true when Generation changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 2,
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

		It("returns true when DeletionTimestamp appears", func() {
			now := metav1.Now()
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					Generation:        1,
					DeletionTimestamp: &now,
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

		It("returns true when ReplicatedStorageClassLabelKey changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-2",
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

		It("returns true when Finalizers change", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
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

		It("returns true when nil ObjectNew", func() {
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				},
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when ReplicatedStorageClassLabelKey appears", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
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

		It("returns true when ReplicatedStorageClassLabelKey disappears", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
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

		It("returns false when only status changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationGeneration: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationGeneration: 2,
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
	})
})

var _ = Describe("rscPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rscPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when ConfigurationGeneration changes", func() {
			oldRSC := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rsc-1",
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 1,
				},
			}
			newRSC := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rsc-1",
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSC,
				ObjectNew: newRSC,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when ConfigurationGeneration is the same", func() {
			oldRSC := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 1,
				},
			}
			newRSC := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 2,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 1,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSC,
				ObjectNew: newRSC,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when type assertion fails (conservative)", func() {
			// Pass a non-RSC object.
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
	})
})

var _ = Describe("rvaPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rvaPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when Attached condition status changes", func() {
			oldRVA := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rva-1",
				},
				Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
							Status: metav1.ConditionFalse,
						},
					},
				},
			}
			newRVA := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rva-1",
				},
				Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVA,
				ObjectNew: newRVA,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when Attached condition reason changes but status is the same", func() {
			oldRVA := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rva-1",
				},
				Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
							Status: metav1.ConditionFalse,
							Reason: "Reason1",
						},
					},
				},
			}
			newRVA := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rva-1",
				},
				Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
							Status: metav1.ConditionFalse,
							Reason: "Reason2",
						},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVA,
				ObjectNew: newRVA,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when type assertion fails (conservative)", func() {
			// DRBDResourceOperation does not implement StatusConditionObject.
			oldObj := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{Name: "op-1"},
				Spec: v1alpha1.DRBDResourceOperationSpec{
					DRBDResourceName: "res-1",
					Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				},
			}
			newObj := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{Name: "op-1"},
				Spec: v1alpha1.DRBDResourceOperationSpec{
					DRBDResourceName: "res-1",
					Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldObj,
				ObjectNew: newObj,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})
})

var _ = Describe("drbdrOpPredicates", func() {
	Describe("CreateFunc", func() {
		var preds []func(event.TypedCreateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := drbdrOpPredicates()
			preds = make([]func(event.TypedCreateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.CreateFunc != nil {
					preds = append(preds, fp.CreateFunc)
				}
			}
		})

		It("returns true when name ends with -formation", func() {
			e := event.TypedCreateEvent[client.Object]{
				Object: &v1alpha1.DRBDResourceOperation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-1-formation",
					},
				},
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when name does not end with -formation", func() {
			e := event.TypedCreateEvent[client.Object]{
				Object: &v1alpha1.DRBDResourceOperation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-1-resize",
					},
				},
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})
	})

	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := drbdrOpPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns false when name does not end with -formation", func() {
			oldOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1-resize",
					Generation: 1,
				},
			}
			newOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1-resize",
					Generation: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldOp,
				ObjectNew: newOp,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when Generation changes", func() {
			oldOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1-formation",
					Generation: 1,
				},
			}
			newOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1-formation",
					Generation: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldOp,
				ObjectNew: newOp,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when Status.Phase changes", func() {
			oldOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
				Status: &v1alpha1.DRBDResourceOperationStatus{
					Phase: v1alpha1.DRBDOperationPhasePending,
				},
			}
			newOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
				Status: &v1alpha1.DRBDResourceOperationStatus{
					Phase: v1alpha1.DRBDOperationPhaseRunning,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldOp,
				ObjectNew: newOp,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when type assertion fails (conservative)", func() {
			// Pass a non-DRBDResourceOperation object with a "-formation" name.
			oldObj := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
			}
			newObj := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldObj,
				ObjectNew: newObj,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when both Status are nil and Generation is the same", func() {
			oldOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
			}
			newOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldOp,
				ObjectNew: newOp,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when old Status is nil and new Status has Phase", func() {
			oldOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
			}
			newOp := &v1alpha1.DRBDResourceOperation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1-formation",
				},
				Status: &v1alpha1.DRBDResourceOperationStatus{
					Phase: v1alpha1.DRBDOperationPhasePending,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldOp,
				ObjectNew: newOp,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})

	Describe("DeleteFunc", func() {
		var preds []func(event.TypedDeleteEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := drbdrOpPredicates()
			preds = make([]func(event.TypedDeleteEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.DeleteFunc != nil {
					preds = append(preds, fp.DeleteFunc)
				}
			}
		})

		It("returns true when name ends with -formation", func() {
			e := event.TypedDeleteEvent[client.Object]{
				Object: &v1alpha1.DRBDResourceOperation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-1-formation",
					},
				},
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when name does not end with -formation", func() {
			e := event.TypedDeleteEvent[client.Object]{
				Object: &v1alpha1.DRBDResourceOperation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-1-resize",
					},
				},
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})
	})
})

var _ = Describe("rvrPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rvrPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when DatameshRevision changes", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					DatameshRevision: 1,
				},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					DatameshRevision: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when DeletionTimestamp appears", func() {
			now := metav1.Now()
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rvr-1",
					DeletionTimestamp: &now,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when Finalizers change", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Finalizers: []string{"test-finalizer"},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when no relevant fields change", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Finalizers: []string{"test-finalizer"},
				},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					DatameshRevision: 1,
				},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Generation: 2, // Generation change alone does not trigger (not in predicate).
					Finalizers: []string{"test-finalizer"},
				},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					DatameshRevision: 1,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when type assertion fails (conservative)", func() {
			// Pass a non-RVR object.
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
	})
})
