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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

var _ = Describe("mapSourceRVRToCloneRVRs", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeCloneRV := func(name, sourceRVName string) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				DataSource: &v1alpha1.VolumeDataSource{
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolume,
					Name: sourceRVName,
				},
			},
		}
	}

	buildClient := func(objs ...client.Object) client.Client {
		b := fake.NewClientBuilder().WithScheme(scheme)
		b = testhelpers.WithRVByDataSourceVolumeNameIndex(b)
		b = testhelpers.WithRVRByReplicatedVolumeNameIndex(b)
		return b.WithObjects(objs...).Build()
	}

	It("returns nil when no clone RV references the source", func() {
		srcRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-src-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-src"},
		}
		cl := buildClient(srcRVR)

		reqs := mapSourceRVRToCloneRVRs(cl)(context.Background(), srcRVR)
		Expect(reqs).To(BeEmpty())
	})

	It("enqueues all RVRs of every clone RV that references the source", func() {
		srcRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-src-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-src"},
		}
		cloneA := makeCloneRV("rv-clone-a", "rv-src")
		cloneB := makeCloneRV("rv-clone-b", "rv-src")
		unrelated := makeCloneRV("rv-clone-c", "rv-other")

		cloneARVR0 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-clone-a-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-clone-a"},
		}
		cloneARVR1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-clone-a-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-clone-a"},
		}
		cloneBRVR0 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-clone-b-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-clone-b"},
		}
		unrelatedRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-clone-c-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-clone-c"},
		}

		cl := buildClient(
			srcRVR,
			cloneA, cloneB, unrelated,
			cloneARVR0, cloneARVR1, cloneBRVR0, unrelatedRVR,
		)

		reqs := mapSourceRVRToCloneRVRs(cl)(context.Background(), srcRVR)

		names := make([]string, 0, len(reqs))
		for _, r := range reqs {
			names = append(names, r.Name)
		}
		Expect(names).To(ConsistOf("rv-clone-a-0", "rv-clone-a-1", "rv-clone-b-0"))
	})

	It("returns nil when source RVR has empty ReplicatedVolumeName", func() {
		srcRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-src-0"},
		}
		cl := buildClient(srcRVR)

		reqs := mapSourceRVRToCloneRVRs(cl)(context.Background(), srcRVR)
		Expect(reqs).To(BeEmpty())
	})

	It("ignores snapshot-kind data sources", func() {
		srcRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-src-0"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-src"},
		}
		snapClone := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-from-snap"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				DataSource: &v1alpha1.VolumeDataSource{
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolumeSnapshot,
					Name: "rv-src",
				},
			},
		}
		cl := buildClient(srcRVR, snapClone)

		reqs := mapSourceRVRToCloneRVRs(cl)(context.Background(), srcRVR)
		Expect(reqs).To(BeEmpty())
	})
})

var _ = Describe("cloneSourceRVRPredicates", func() {
	var pred predicate.Predicate

	BeforeEach(func() {
		preds := cloneSourceRVRPredicates()
		Expect(preds).To(HaveLen(1))
		pred = preds[0]
	})

	makeRVR := func(name, node string, state v1alpha1.DiskState) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: node, ReplicatedVolumeName: "rv-src"},
		}
		if state != "" {
			rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: state}
		}
		return rvr
	}

	It("accepts Create and Delete events", func() {
		rvr := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		Expect(pred.Create(event.CreateEvent{Object: rvr})).To(BeTrue())
		Expect(pred.Delete(event.DeleteEvent{Object: rvr})).To(BeTrue())
	})

	It("rejects generic events", func() {
		rvr := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		Expect(pred.Generic(event.GenericEvent{Object: rvr})).To(BeFalse())
	})

	It("rejects updates without meaningful changes", func() {
		oldRVR := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		newRVR := oldRVR.DeepCopy()
		newRVR.ResourceVersion = "42"
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldRVR, ObjectNew: newRVR})).To(BeFalse())
	})

	It("accepts NodeName changes", func() {
		oldRVR := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		newRVR := oldRVR.DeepCopy()
		newRVR.Spec.NodeName = "node-2"
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldRVR, ObjectNew: newRVR})).To(BeTrue())
	})

	It("accepts BackingVolume.State changes", func() {
		oldRVR := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateInconsistent)
		newRVR := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldRVR, ObjectNew: newRVR})).To(BeTrue())
	})

	It("accepts transition from absent BackingVolume to populated one", func() {
		oldRVR := makeRVR("rv-src-0", "node-1", "")
		newRVR := makeRVR("rv-src-0", "node-1", v1alpha1.DiskStateUpToDate)
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldRVR, ObjectNew: newRVR})).To(BeTrue())
	})
})
