/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for more information.
*/

package rvr_scheduling_controller_test

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
)

var _ = Describe("RvrSchedulingController Reconciler", Ordered, func() {
	var (
		ctx     context.Context
		scheme  *runtime.Scheme
		builder *fake.ClientBuilder
		cl      client.WithWatch
		rec     *rvrschedulingcontroller.Reconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(snc.AddToScheme(scheme))
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
		utilruntime.Must(v1alpha3.AddToScheme(scheme))
		builder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
	})

	Context("Diskful & Local phase", func() {
		It("schedules diskful replicas on all publishOn nodes when topology and storage pool allow it", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-local",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-local"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Local",
					Topology:     "Zonal",
					Zones:        []string{"zone-a"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "vg-a"},
					},
				},
			}

			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "vg-a"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-a"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Nodes: []snc.LVMVolumeGroupNode{
						{Name: "node-a"},
						{Name: "node-b"},
					},
				},
			}

			rvr1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvr2 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rsp, lvg, rvr1, rvr2).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
			Expect(updatedRVR1.Spec.NodeName).To(Equal("node-a"))

			updatedRVR2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-2"}, updatedRVR2)).To(Succeed())
			Expect(updatedRVR2.Spec.NodeName).To(Equal("node-b"))
		})

		It("returns error when there are not enough diskful replicas to cover all publishOn nodes", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-local",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-local"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Local",
					Topology:     "Zonal",
					Zones:        []string{"zone-a"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "vg-a"},
					},
				},
			}

			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "vg-a"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-a"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Nodes: []snc.LVMVolumeGroupNode{
						{Name: "node-a"},
						{Name: "node-b"},
					},
				},
			}

			// Только одна Diskful-реплика при двух publishOn-нодах.
			rvr1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rsp, lvg, rvr1).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not enough Diskful replicas to cover publishOn nodes"))
		})

		It("returns error for Zonal topology when publishOn nodes span multiple zones", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-local-zonal-error"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-local-zonal-error",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-local-zonal-error"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Local",
					Topology:     "Zonal",
					Zones:        []string{"zone-a", "zone-b"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}

			// Уже есть Diskful в zone-a, publishOn включает ноду в zone-b — должна быть ошибка Zonal topology.
			rvrExisting := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-local-zonal-existing"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-local-zonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvrExtra := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-local-zonal-extra"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-local-zonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrExisting, rvrExtra).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot satisfy Zonal topology"))
		})

		It("returns error for TransZonal topology when publishOn node is in an already used zone", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-local-transzonal-error"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-local-transzonal-error",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-local-transzonal-error"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Local",
					Topology:     "TransZonal",
					Zones:        []string{"zone-a"},
				},
			}

			// Обе publishOn-ноды в одной зоне.
			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			// Уже есть Diskful в zone-a, publishOn включает вторую ноду в той же зоне — должна быть ошибка TransZonal.
			rvrExisting := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-local-transzonal-existing"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-local-transzonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvrExtra := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-local-transzonal-extra"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-local-transzonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrExisting, rvrExtra).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot satisfy TransZonal topology"))
		})
	})

	Context("Diskful phase (non-Local)", func() {
		It("schedules diskful replicas on publishOn nodes allowed by storage pool for Any topology", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-diskful-any"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-diskful-any",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-diskful-any"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-any",
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-any"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: "LVM",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "vg-b"},
					},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			// LVMVolumeGroup только на node-b, значит candidate для Diskful — только node-b.
			lvgB := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "vg-b"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-b"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Nodes: []snc.LVMVolumeGroupNode{
						{Name: "node-b"},
					},
				},
			}

			// Одна несScheduled Diskful-реплика.
			rvr1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-any-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-any",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, rsp, nodeA, nodeB, lvgB, rvr1).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-any-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-b"))
		})

		It("keeps all diskful replicas in a single zone for Zonal topology", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-diskful-zonal"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-diskful-zonal",
					PublishOn:                  []string{"node-a", "node-b", "node-c"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-diskful-zonal"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Zonal",
					Zones:        []string{"zone-a", "zone-b"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeC := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-c",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}

			// Уже есть Diskful в zone-a, несScheduled должен попасть тоже в zone-a (node-b), а не в node-c.
			existing := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-zonal-existing"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-zonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvr1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-zonal-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-zonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, nodeC, existing, rvr1).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-zonal-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-b"))
		})

		It("distributes diskful replicas across zones for TransZonal topology", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-diskful-transzonal"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-diskful-transzonal",
					PublishOn:                  []string{"node-a", "node-b", "node-c"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-diskful-transzonal"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "TransZonal",
					Zones:        []string{"zone-a", "zone-b", "zone-c"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}
			nodeC := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-c",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-c"},
				},
			}

			// Уже есть Diskful в zone-a, две несScheduled должны попасть в zone-b и zone-c.
			existing := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-transzonal-existing"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-transzonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvr1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-transzonal-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-transzonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}
			rvr2 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-transzonal-2"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-diskful-transzonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, nodeC, existing, rvr1, rvr2).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha3.ReplicatedVolumeReplica{}
			updated2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-transzonal-1"}, updated1)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-transzonal-2"}, updated2)).To(Succeed())

			nodeNames := []string{updated1.Spec.NodeName, updated2.Spec.NodeName}
			Expect(nodeNames).To(ContainElement("node-b"))
			Expect(nodeNames).To(ContainElement("node-c"))
		})
	})

	Context("Access phase", func() {
		It("schedules access replicas only on publishOn nodes without any replicas", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-access"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-access",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-access"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			// Уже есть Diskful-реплика на node-a, её нужно исключить из кандидатов.
			diskful := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}

			access1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access",
					Type:                 "Access",
				},
			}
			access2 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-2"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access",
					Type:                 "Access",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, diskful, access1, access2).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updatedAccess1 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-1"}, updatedAccess1)).To(Succeed())
			updatedAccess2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-2"}, updatedAccess2)).To(Succeed())

			nodeNames := []string{updatedAccess1.Spec.NodeName, updatedAccess2.Spec.NodeName}
			// Ровно одна Access-реплика должна быть на node-b, вторая может остаться не размещённой.
			Expect(nodeNames).To(ContainElement("node-b"))
			Expect(nodeNames).To(ContainElement(""))
		})

		It("does nothing when all publishOn nodes already have replicas", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-access-full"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-access-full",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-access-full"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			// На обеих publishOn-нодах уже есть реплики (Diskful/Access).
			rvrA := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access-full",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvrB := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-b"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access-full",
					Type:                 "Access",
					NodeName:             "node-b",
				},
			}

			access := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-unscheduled"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-access-full",
					Type:                 "Access",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrA, rvrB, access).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updatedAccess := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-unscheduled"}, updatedAccess)).To(Succeed())
			Expect(updatedAccess.Spec.NodeName).To(Equal(""))
		})

		It("sets Scheduled=True for replicas with nodeName and Scheduled=False for others", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-conditions"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-conditions",
					PublishOn:                  []string{"node-a"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-conditions"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Any",
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			// Одна реплика уже запланирована, вторая — нет.
			scheduled := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-scheduled"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-conditions",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
			}

			unscheduled := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-unscheduled"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-conditions",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
				WithRuntimeObjects(rv, rsc, nodeA, scheduled, unscheduled).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updatedScheduled := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-scheduled"}, updatedScheduled)).To(Succeed())
			condScheduled := meta.FindStatusCondition(updatedScheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
			Expect(condScheduled).ToNot(BeNil())
			Expect(condScheduled.Status).To(Equal(metav1.ConditionTrue))
			Expect(condScheduled.Reason).To(Equal(v1alpha3.ReasonReplicaScheduled))

			updatedUnscheduled := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-unscheduled"}, updatedUnscheduled)).To(Succeed())
			condUnscheduled := meta.FindStatusCondition(updatedUnscheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
			Expect(condUnscheduled).ToNot(BeNil())
			Expect(condUnscheduled.Status).To(Equal(metav1.ConditionFalse))
			Expect(condUnscheduled.Reason).To(Equal(v1alpha3.ReasonWaitingForAnotherReplica))
		})
	})

	Context("TieBreaker phase", func() {
		It("distributes tiebreaker replicas across zones for TransZonal topology", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-tb-transzonal"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-tb-transzonal",
					PublishOn:                  []string{"node-a", "node-b", "node-c"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-tb-transzonal"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "TransZonal",
					Zones:        []string{"zone-a", "zone-b", "zone-c"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}
			nodeC := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-c",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-c"},
				},
			}

			// Уже есть Diskful в zone-a, две несScheduled TieBreaker должны разойтись по zone-b и zone-c.
			existing := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-existing"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			tb1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal",
					Type:                 "TieBreaker",
				},
			}
			tb2 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-2"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal",
					Type:                 "TieBreaker",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, nodeC, existing, tb1, tb2).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha3.ReplicatedVolumeReplica{}
			updated2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-1"}, updated1)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-2"}, updated2)).To(Succeed())

			Expect(updated1.Spec.NodeName).ToNot(Equal(""))
			Expect(updated2.Spec.NodeName).ToNot(Equal(""))
			Expect(updated1.Spec.NodeName).ToNot(Equal(updated2.Spec.NodeName))
		})

		It("keeps all tiebreaker replicas in a single zone for Zonal topology", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-tb-zonal"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-tb-zonal",
					PublishOn:                  []string{"node-a", "node-b"},
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-tb-zonal"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Zonal",
					Zones:        []string{"zone-a"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}

			tb1 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-zonal-1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-zonal",
					Type:                 "TieBreaker",
				},
			}
			tb2 := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-zonal-2"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-zonal",
					Type:                 "TieBreaker",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, tb1, tb2).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha3.ReplicatedVolumeReplica{}
			updated2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-zonal-1"}, updated1)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb-zonal-2"}, updated2)).To(Succeed())

			Expect(updated1.Spec.NodeName).ToNot(Equal(""))
			Expect(updated2.Spec.NodeName).ToNot(Equal(""))
		})

		It("returns error for TransZonal topology when there are no free nodes in minimal replica zones", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-tb-transzonal-error"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-tb-transzonal-error",
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-tb-transzonal-error"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "TransZonal",
					Zones:        []string{"zone-a", "zone-b"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}

			rvrA := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-a-transzonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvrB := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-b-transzonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-b",
				},
			}

			tb := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-transzonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-transzonal-error",
					Type:                 "TieBreaker",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrA, rvrB, tb).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot schedule TieBreaker: no free node in zones with minimal replica count"))
		})

		It("returns error for Zonal topology when existing replicas are already in multiple zones", func() {
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-tb-zonal-error"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-tb-zonal-error",
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha3.ConditionTypeReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-tb-zonal-error"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: "Any",
					Topology:     "Zonal",
					Zones:        []string{"zone-a", "zone-b"},
				},
			}

			nodeA := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
				},
			}
			nodeB := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-b",
					Labels: map[string]string{"topology.kubernetes.io/zone": "zone-b"},
				},
			}

			rvrA := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-a-zonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-zonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-a",
				},
			}
			rvrB := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-b-zonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-zonal-error",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-b",
				},
			}

			tb := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-tb-zonal-error"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-tb-zonal-error",
					Type:                 "TieBreaker",
				},
			}

			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(rv, rsc, nodeA, nodeB, rvrA, rvrB, tb).
				Build()
			rec = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot satisfy Zonal topology: replicas already exist in multiple zones"))
		})
	})
})
