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
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("in-place TB scheduling", func() {
	It("repairs a removed original voter before admitting the TB's resync at limit=1", func(ctx SpecContext) {
		rv := newRV(defaultConfig())
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rv.Status.DatameshRevision = 10
		rv.Status.EffectiveLayout.UpToDateVoters = 1
		var nodes []v1alpha1.ReplicatedStoragePoolEligibleNode
		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		objects := []client.Object{rv}
		for i := range 3 {
			node, zone, lvg := fmt.Sprintf("node-%d", i), fmt.Sprintf("zone-%d", i), fmt.Sprintf("vg-%d", i)
			nodes = append(nodes, makeNode(node, zone, makeLVG(lvg, true)))
			rvr := newRVR(uint8(i), v1alpha1.ReplicaTypeDiskful)
			if i < 2 {
				rvr.Spec.NodeName = node
				rvr.Status.DatameshRevision = 10
				memberType := v1alpha1.DatameshMemberTypeTieBreaker
				if i == 0 {
					// The TB sorts before the repair: giving it storage first
					// would reserve the sole sender end and block recovery.
					rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskless
				} else {
					memberType = v1alpha1.DatameshMemberTypeDiskful
					rvr.Spec.LVMVolumeGroupName = lvg
					rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: v1alpha1.DiskStateUpToDate}
				}
				rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{Name: rvr.Name, NodeName: node, Type: memberType, Zone: zone})
			}
			rvrs = append(rvrs, rvr)
			objects = append(objects, rvr)
		}
		objects = append(objects, newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodes))
		scheme := newScheme()
		cl := newClientBuilder(scheme).WithObjects(objects...).WithStatusSubresource(rvrs[0], rvrs[1], rvrs[2]).Build()
		rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})
		rec.resyncLimit = 1

		_, err := reconcileRV(ctx, rec)
		Expect(err).NotTo(HaveOccurred())
		Expect(getUpdatedRVR(ctx, cl, rvrs[0]).Spec.LVMVolumeGroupName).To(BeEmpty())
		repair := getUpdatedRVR(ctx, cl, rvrs[2])
		Expect(repair.Spec.NodeName).To(Equal("node-2"))
		Expect(repair.Spec.LVMVolumeGroupName).To(Equal("vg-2"))
		expectScheduledCondition(ctx, cl, repair, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
	})

	DescribeTable("admits a full resync on the assigned TB node",
		func(ctx SpecContext, occupiedNode string, topology v1alpha1.ReplicatedStorageClassTopology, scheduled bool) {
			rv := newRV(defaultConfig())
			rv.Status.Configuration.Topology = topology
			rv.Status.DatameshRevision = 10
			rv.Status.EffectiveLayout.UpToDateVoters = 2
			var nodes []v1alpha1.ReplicatedStoragePoolEligibleNode
			var rvrs []*v1alpha1.ReplicatedVolumeReplica
			objects := []client.Object{rv}
			for i := range 3 {
				node, zone, lvg := fmt.Sprintf("node-%d", i), fmt.Sprintf("zone-%d", i), fmt.Sprintf("vg-%d", i)
				nodes = append(nodes, makeNode(node, zone, makeLVG(lvg, true)))
				rvr := newRVR(uint8(i), v1alpha1.ReplicaTypeDiskful)
				rvr.Spec.NodeName = node
				rvr.Status.DatameshRevision = 10
				memberType := v1alpha1.DatameshMemberTypeDiskful
				if i < 2 {
					rvr.Spec.LVMVolumeGroupName = lvg
					rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: v1alpha1.DiskStateUpToDate}
				} else {
					memberType = v1alpha1.DatameshMemberTypeTieBreaker
					rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskless
				}
				rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{Name: rvr.Name, NodeName: node, Type: memberType, Zone: zone})
				rvrs = append(rvrs, rvr)
				objects = append(objects, rvr)
			}
			objects = append(objects, newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nodes))
			if occupiedNode != "" {
				objects = append(objects, &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "other-0"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "other", Type: v1alpha1.ReplicaTypeDiskful, NodeName: occupiedNode, LVMVolumeGroupName: "other-vg"},
				})
			}
			scheme := newScheme()
			cl := newClientBuilder(scheme).WithObjects(objects...).WithStatusSubresource(rvrs[0], rvrs[1], rvrs[2]).Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})
			rec.resyncLimit = 1
			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			updated := getUpdatedRVR(ctx, cl, rvrs[2])
			Expect(updated.Spec.NodeName).To(Equal("node-2"))
			if scheduled {
				Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-2"))
				expectScheduledCondition(ctx, cl, rvrs[2], metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
			} else {
				Expect(updated.Spec.LVMVolumeGroupName).To(BeEmpty())
				expectScheduledCondition(ctx, cl, rvrs[2], metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot)
			}
		},
		Entry("does not block against its own pre-admission end at limit=1", "", v1alpha1.TopologyIgnored, true),
		Entry("waits when a sender is full", "node-0", v1alpha1.TopologyIgnored, false),
		Entry("waits when the receiver is full", "node-2", v1alpha1.TopologyIgnored, false),
		Entry("does not count the promoting TB against its own TransZonal placement", "", v1alpha1.TopologyTransZonal, true),
	)
})
