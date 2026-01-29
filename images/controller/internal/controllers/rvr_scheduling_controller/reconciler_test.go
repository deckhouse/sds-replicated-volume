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

package rvrschedulingcontroller_test

import (
	"context"
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
	testhelpers "github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

type ClusterSetup struct {
	Name         string
	Zones        []string
	RSCZones     []string
	NodesPerZone int
	NodeScores   map[string]int
}

type ExistingReplica struct {
	Type     v1alpha1.ReplicaType
	NodeName string
}

type ReplicasToSchedule struct {
	Diskful    int
	TieBreaker int
}

type ExpectedResult struct {
	Error                       string
	DiskfulZones                []string
	TieBreakerZones             []string
	DiskfulNodes                []string
	TieBreakerNodes             []string
	ScheduledDiskfulCount       *int
	UnscheduledDiskfulCount     *int
	UnscheduledReason           string
	ScheduledTieBreakerCount    *int
	UnscheduledTieBreakerCount  *int
	UnscheduledTieBreakerReason string
}

type IntegrationTestCase struct {
	Name       string
	Cluster    string
	Topology   v1alpha1.ReplicatedStorageClassTopology
	AttachTo   []string
	Existing   []ExistingReplica
	ToSchedule ReplicasToSchedule
	Expected   ExpectedResult
}

func intPtr(i int) *int {
	return &i
}

type mockExtenderClient struct {
	scores map[string]int
}

func (m *mockExtenderClient) QueryLVGScores(
	_ context.Context,
	lvgs []rvrschedulingcontroller.LVGQuery,
	_ rvrschedulingcontroller.VolumeInfo,
) (map[string]int, error) {
	result := make(map[string]int)
	for _, lvg := range lvgs {
		if score, ok := m.scores[lvg.Name]; ok {
			result[lvg.Name] = score
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no LVGs with capacity")
	}
	return result, nil
}

func generateEligibleNodes(setup ClusterSetup) ([]v1alpha1.ReplicatedStoragePoolEligibleNode, map[string]int) {
	var eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
	scores := make(map[string]int)

	for _, zone := range setup.Zones {
		for i := 1; i <= setup.NodesPerZone; i++ {
			nodeName := fmt.Sprintf("node-%s%d", zone[len(zone)-1:], i)
			lvgName := fmt.Sprintf("vg-%s", nodeName)

			node := v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   nodeName,
				ZoneName:   zone,
				NodeReady:  true,
				AgentReady: true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{
						Name:  lvgName,
						Ready: true,
					},
				},
			}
			eligible = append(eligible, node)

			if score, ok := setup.NodeScores[nodeName]; ok {
				scores[lvgName] = score
			} else {
				scores[lvgName] = 100 - (len(eligible)-1)*10
			}
		}
	}
	return eligible, scores
}

func generateRSCAndRSP(rscName, rspName string, topology v1alpha1.ReplicatedStorageClassTopology, zones []string, eligible []v1alpha1.ReplicatedStoragePoolEligibleNode) (*v1alpha1.ReplicatedStorageClass, *v1alpha1.ReplicatedStoragePool) {
	rsc := &v1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: rscName},
		Spec: v1alpha1.ReplicatedStorageClassSpec{
			Storage: v1alpha1.ReplicatedStorageClassStorage{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "vg-1"},
				},
			},
			VolumeAccess: v1alpha1.VolumeAccessAny,
			Topology:     topology,
			Zones:        zones,
		},
		Status: v1alpha1.ReplicatedStorageClassStatus{
			StoragePoolName: rspName,
		},
	}

	rsp := &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: rspName},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "vg-1"},
			},
		},
		Status: v1alpha1.ReplicatedStoragePoolStatus{
			EligibleNodes: eligible,
		},
	}

	return rsc, rsp
}

var clusterConfigs = map[string]ClusterSetup{
	"small-1z": {
		Name:         "small-1z",
		Zones:        []string{"zone-a"},
		RSCZones:     []string{"zone-a"},
		NodesPerZone: 2,
		NodeScores:   map[string]int{"node-a1": 100, "node-a2": 80},
	},
	"small-1z-4n": {
		Name:         "small-1z-4n",
		Zones:        []string{"zone-a"},
		RSCZones:     []string{"zone-a"},
		NodesPerZone: 4,
		NodeScores:   map[string]int{"node-a1": 100, "node-a2": 90, "node-a3": 80, "node-a4": 70},
	},
	"medium-2z": {
		Name:         "medium-2z",
		Zones:        []string{"zone-a", "zone-b"},
		RSCZones:     []string{"zone-a", "zone-b"},
		NodesPerZone: 2,
		NodeScores:   map[string]int{"node-a1": 100, "node-a2": 80, "node-b1": 90, "node-b2": 70},
	},
	"medium-2z-4n": {
		Name:         "medium-2z-4n",
		Zones:        []string{"zone-a", "zone-b"},
		RSCZones:     []string{"zone-a", "zone-b"},
		NodesPerZone: 4,
		NodeScores: map[string]int{
			"node-a1": 100, "node-a2": 90, "node-a3": 80, "node-a4": 70,
			"node-b1": 95, "node-b2": 85, "node-b3": 75, "node-b4": 65,
		},
	},
	"large-3z": {
		Name:         "large-3z",
		Zones:        []string{"zone-a", "zone-b", "zone-c"},
		RSCZones:     []string{"zone-a", "zone-b", "zone-c"},
		NodesPerZone: 2,
		NodeScores: map[string]int{
			"node-a1": 100, "node-a2": 80,
			"node-b1": 90, "node-b2": 70,
			"node-c1": 85, "node-c2": 65,
		},
	},
	"large-3z-3n": {
		Name:         "large-3z-3n",
		Zones:        []string{"zone-a", "zone-b", "zone-c"},
		RSCZones:     []string{"zone-a", "zone-b", "zone-c"},
		NodesPerZone: 3,
		NodeScores: map[string]int{
			"node-a1": 100, "node-a2": 90, "node-a3": 80,
			"node-b1": 95, "node-b2": 85, "node-b3": 75,
			"node-c1": 92, "node-c2": 82, "node-c3": 72,
		},
	},
	"xlarge-4z": {
		Name:         "xlarge-4z",
		Zones:        []string{"zone-a", "zone-b", "zone-c", "zone-d"},
		RSCZones:     []string{"zone-a", "zone-b", "zone-c"},
		NodesPerZone: 2,
		NodeScores: map[string]int{
			"node-a1": 100, "node-a2": 80,
			"node-b1": 90, "node-b2": 70,
			"node-c1": 85, "node-c2": 65,
			"node-d1": 95, "node-d2": 75,
		},
	},
}

var _ = Describe("RVR Scheduling Integration Tests", Ordered, func() {
	var (
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
	})

	runTestCase := func(ctx context.Context, tc IntegrationTestCase) {
		cluster := clusterConfigs[tc.Cluster]
		Expect(cluster.Name).ToNot(BeEmpty(), "Unknown cluster: %s", tc.Cluster)

		eligibleNodes, scores := generateEligibleNodes(cluster)

		rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", tc.Topology, cluster.RSCZones, eligibleNodes)

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-test",
				Finalizers: []string{v1alpha1.ControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-test",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DesiredAttachTo: tc.AttachTo,
				Conditions: []metav1.Condition{{
					Type:   v1alpha1.ReplicatedVolumeCondIOReadyType,
					Status: metav1.ConditionTrue,
				}},
			},
		}

		var rvrList []*v1alpha1.ReplicatedVolumeReplica
		rvrIndex := 1

		for _, existing := range tc.Existing {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-existing-%d", rvrIndex)},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 existing.Type,
					NodeName:             existing.NodeName,
				},
			}
			rvrList = append(rvrList, rvr)
			rvrIndex++
		}

		for i := 0; i < tc.ToSchedule.Diskful; i++ {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-diskful-%d", i+1)},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}
			rvrList = append(rvrList, rvr)
		}

		for i := 0; i < tc.ToSchedule.TieBreaker; i++ {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-tiebreaker-%d", i+1)},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeTieBreaker,
				},
			}
			rvrList = append(rvrList, rvr)
		}

		objects := []runtime.Object{rv, rsc, rsp}
		for _, rvr := range rvrList {
			objects = append(objects, rvr)
		}

		cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
			WithScheme(scheme)).
			WithRuntimeObjects(objects...).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			Build()

		extender := &mockExtenderClient{scores: scores}
		rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

		_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})

		if tc.Expected.Error != "" {
			Expect(err).To(HaveOccurred(), "Expected error but got none")
			Expect(err.Error()).To(ContainSubstring(tc.Expected.Error), "Error message mismatch")
			return
		}

		Expect(err).ToNot(HaveOccurred(), "Unexpected error: %v", err)

		var scheduledDiskful []string
		var unscheduledDiskful []string
		var diskfulZones []string
		for i := 0; i < tc.ToSchedule.Diskful; i++ {
			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-diskful-%d", i+1)}, updated)).To(Succeed())

			if updated.Spec.NodeName != "" {
				scheduledDiskful = append(scheduledDiskful, updated.Spec.NodeName)
				for _, node := range eligibleNodes {
					if node.NodeName == updated.Spec.NodeName {
						if !slices.Contains(diskfulZones, node.ZoneName) {
							diskfulZones = append(diskfulZones, node.ZoneName)
						}
						break
					}
				}
			} else {
				unscheduledDiskful = append(unscheduledDiskful, updated.Name)
				if tc.Expected.UnscheduledReason != "" {
					cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
					Expect(cond).ToNot(BeNil(), "Unscheduled replica %s should have Scheduled condition", updated.Name)
					Expect(cond.Status).To(Equal(metav1.ConditionFalse), "Unscheduled replica %s should have Scheduled=False", updated.Name)
					Expect(cond.Reason).To(Equal(tc.Expected.UnscheduledReason), "Unscheduled replica %s has wrong reason", updated.Name)
				}
			}
		}

		if tc.Expected.ScheduledDiskfulCount != nil {
			Expect(len(scheduledDiskful)).To(Equal(*tc.Expected.ScheduledDiskfulCount), "Scheduled Diskful count mismatch")
		} else if tc.Expected.UnscheduledDiskfulCount == nil {
			Expect(len(unscheduledDiskful)).To(Equal(0), "All Diskful replicas should be scheduled, but %d were not: %v", len(unscheduledDiskful), unscheduledDiskful)
		}
		if tc.Expected.UnscheduledDiskfulCount != nil {
			Expect(len(unscheduledDiskful)).To(Equal(*tc.Expected.UnscheduledDiskfulCount), "Unscheduled Diskful count mismatch")
		}

		if tc.Expected.DiskfulZones != nil {
			Expect(diskfulZones).To(ConsistOf(tc.Expected.DiskfulZones), "Diskful zones mismatch")
		}

		if tc.Expected.DiskfulNodes != nil {
			Expect(scheduledDiskful).To(ConsistOf(tc.Expected.DiskfulNodes), "Diskful nodes mismatch")
		}

		var scheduledTieBreaker []string
		var unscheduledTieBreaker []string
		var tieBreakerZones []string
		for i := 0; i < tc.ToSchedule.TieBreaker; i++ {
			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-tiebreaker-%d", i+1)}, updated)).To(Succeed())
			if updated.Spec.NodeName != "" {
				scheduledTieBreaker = append(scheduledTieBreaker, updated.Spec.NodeName)

				for _, node := range eligibleNodes {
					if node.NodeName == updated.Spec.NodeName {
						if !slices.Contains(tieBreakerZones, node.ZoneName) {
							tieBreakerZones = append(tieBreakerZones, node.ZoneName)
						}
						break
					}
				}
			} else {
				unscheduledTieBreaker = append(unscheduledTieBreaker, updated.Name)
				if tc.Expected.UnscheduledTieBreakerReason != "" {
					cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
					Expect(cond).ToNot(BeNil(), "Unscheduled TieBreaker replica %s should have Scheduled condition", updated.Name)
					Expect(cond.Status).To(Equal(metav1.ConditionFalse), "Unscheduled TieBreaker replica %s should have Scheduled=False", updated.Name)
					Expect(cond.Reason).To(Equal(tc.Expected.UnscheduledTieBreakerReason), "Unscheduled TieBreaker replica %s has wrong reason", updated.Name)
				}
			}
		}

		if tc.Expected.ScheduledTieBreakerCount != nil {
			Expect(len(scheduledTieBreaker)).To(Equal(*tc.Expected.ScheduledTieBreakerCount), "Scheduled TieBreaker count mismatch")
		} else if tc.Expected.UnscheduledTieBreakerCount == nil {
			Expect(len(unscheduledTieBreaker)).To(Equal(0), "All TieBreaker replicas should be scheduled, but %d were not: %v", len(unscheduledTieBreaker), unscheduledTieBreaker)
		}
		if tc.Expected.UnscheduledTieBreakerCount != nil {
			Expect(len(unscheduledTieBreaker)).To(Equal(*tc.Expected.UnscheduledTieBreakerCount), "Unscheduled TieBreaker count mismatch")
		}

		if tc.Expected.TieBreakerZones != nil {
			Expect(tieBreakerZones).To(ConsistOf(tc.Expected.TieBreakerZones), "TieBreaker zones mismatch")
		}

		if tc.Expected.TieBreakerNodes != nil {
			Expect(scheduledTieBreaker).To(ConsistOf(tc.Expected.TieBreakerNodes), "TieBreaker nodes mismatch")
		}

		allScheduled := append(scheduledDiskful, scheduledTieBreaker...)
		for _, existing := range tc.Existing {
			allScheduled = append(allScheduled, existing.NodeName)
		}
		nodeCount := make(map[string]int)
		for _, node := range allScheduled {
			nodeCount[node]++
			Expect(nodeCount[node]).To(Equal(1), "Node %s has multiple replicas", node)
		}
	}

	Context("Zonal Topology", func() {
		zonalTestCases := []IntegrationTestCase{
			{
				Name:       "1. small-1z: D:2 - all in zone-a",
				Cluster:    "small-1z",
				Topology:   "Zonal",
				ToSchedule: ReplicasToSchedule{Diskful: 2},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}},
			},
			{
				Name:       "2. small-1z: attachTo node-a1 - D on node-a1",
				Cluster:    "small-1z",
				Topology:   "Zonal",
				AttachTo:   []string{"node-a1"},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulNodes: []string{"node-a1"}, TieBreakerNodes: []string{"node-a2"}},
			},
			{
				Name:       "3. medium-2z: attachTo same zone - all in zone-a",
				Cluster:    "medium-2z",
				Topology:   "Zonal",
				AttachTo:   []string{"node-a1", "node-a2"},
				ToSchedule: ReplicasToSchedule{Diskful: 2},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}},
			},
			{
				Name:       "4. medium-2z-4n: existing D in zone-a - new D and TB in zone-a",
				Cluster:    "medium-2z-4n",
				Topology:   "Zonal",
				Existing:   []ExistingReplica{{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"}},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}, TieBreakerZones: []string{"zone-a"}},
			},
			{
				Name:     "5. medium-2z: existing D in different zones - topology conflict",
				Cluster:  "medium-2z",
				Topology: "Zonal",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1},
				Expected: ExpectedResult{
					ScheduledDiskfulCount:   intPtr(0),
					UnscheduledDiskfulCount: intPtr(1),
					UnscheduledReason:       v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonTopologyConstraintsFailed,
				},
			},
			{
				Name:     "6. small-1z: all nodes occupied - no candidate nodes for TB",
				Cluster:  "small-1z",
				Topology: "Zonal",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{TieBreaker: 1},
				Expected: ExpectedResult{
					ScheduledTieBreakerCount:    intPtr(0),
					UnscheduledTieBreakerCount:  intPtr(1),
					UnscheduledTieBreakerReason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes,
				},
			},
			{
				Name:       "7. medium-2z: TB only without Diskful - no candidate nodes",
				Cluster:    "medium-2z",
				Topology:   "Zonal",
				ToSchedule: ReplicasToSchedule{TieBreaker: 1},
				Expected: ExpectedResult{
					ScheduledTieBreakerCount:    intPtr(0),
					UnscheduledTieBreakerCount:  intPtr(1),
					UnscheduledTieBreakerReason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes,
				},
			},
		}

		for _, tc := range zonalTestCases {
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})

	Context("TransZonal Topology", func() {
		transZonalTestCases := []IntegrationTestCase{
			{
				Name:       "1. large-3z: D:3 - one per zone",
				Cluster:    "large-3z",
				Topology:   "TransZonal",
				ToSchedule: ReplicasToSchedule{Diskful: 3},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a", "zone-b", "zone-c"}},
			},
			{
				Name:     "2. large-3z: existing D in zone-a,b - new D in zone-c",
				Cluster:  "large-3z",
				Topology: "TransZonal",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-c"}},
			},
			{
				Name:     "3. large-3z: existing D in zone-a,b - TB in zone-c",
				Cluster:  "large-3z",
				Topology: "TransZonal",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{TieBreaker: 1},
				Expected:   ExpectedResult{TieBreakerZones: []string{"zone-c"}},
			},
			{
				Name:       "4. medium-2z: existing D in zone-a - new D in zone-b",
				Cluster:    "medium-2z",
				Topology:   "TransZonal",
				Existing:   []ExistingReplica{{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"}},
				ToSchedule: ReplicasToSchedule{Diskful: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-b"}},
			},
			{
				Name:       "5. xlarge-4z: D:3 - D in RSC zones only",
				Cluster:    "xlarge-4z",
				Topology:   "TransZonal",
				ToSchedule: ReplicasToSchedule{Diskful: 3, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a", "zone-b", "zone-c"}},
			},
			{
				Name:     "6. medium-2z: all nodes occupied - no candidate nodes",
				Cluster:  "medium-2z",
				Topology: "TransZonal",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a2"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b2"},
				},
				ToSchedule: ReplicasToSchedule{TieBreaker: 1},
				Expected: ExpectedResult{
					ScheduledTieBreakerCount:    intPtr(0),
					UnscheduledTieBreakerCount:  intPtr(1),
					UnscheduledTieBreakerReason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes,
				},
			},
		}

		for _, tc := range transZonalTestCases {
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})

	Context("Ignored Topology", func() {
		ignoredTestCases := []IntegrationTestCase{
			{
				Name:       "1. large-3z: D:2 - Diskful uses best scores",
				Cluster:    "large-3z",
				Topology:   "Ignored",
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulNodes: []string{"node-a1", "node-b1"}},
			},
			{
				Name:       "2. medium-2z: attachTo - prefer attachTo nodes",
				Cluster:    "medium-2z",
				Topology:   "Ignored",
				AttachTo:   []string{"node-a1", "node-b1"},
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulNodes: []string{"node-a1", "node-b1"}},
			},
			{
				Name:       "3. small-1z-4n: D:2, TB:2 - 4 replicas on 4 nodes",
				Cluster:    "small-1z-4n",
				Topology:   "Ignored",
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 2},
				Expected:   ExpectedResult{},
			},
			{
				Name:     "4. small-1z: all nodes occupied - no candidate nodes",
				Cluster:  "small-1z",
				Topology: "Ignored",
				Existing: []ExistingReplica{
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{TieBreaker: 1},
				Expected: ExpectedResult{
					ScheduledTieBreakerCount:    intPtr(0),
					UnscheduledTieBreakerCount:  intPtr(1),
					UnscheduledTieBreakerReason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes,
				},
			},
		}

		for _, tc := range ignoredTestCases {
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})

	Context("Extender Filtering", func() {
		It("sets Scheduled=False when extender filters out all nodes (no space)", func(ctx SpecContext) {
			cluster := clusterConfigs["medium-2z"]
			eligibleNodes, _ := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty(), "Replica should not be scheduled when no space")
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes))
		})

		It("filters nodes where extender doesn't return LVG", func(ctx SpecContext) {
			cluster := clusterConfigs["medium-2z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			zoneAScores := make(map[string]int)
			for lvg, score := range scores {
				if lvg == "vg-node-a1" || lvg == "vg-node-a2" {
					zoneAScores[lvg] = score
				}
			}
			extender := &mockExtenderClient{scores: zoneAScores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Or(Equal("node-a1"), Equal("node-a2")))
		})
	})
})

var _ = Describe("Partial Scheduling and Edge Cases", Ordered, func() {
	var (
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
	})

	Context("Partial Diskful Scheduling", func() {
		It("schedules as many Diskful replicas as possible and sets Scheduled=False on remaining", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}
			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}
			rvr3 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-3"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr1, rvr2, rvr3).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			var scheduledCount int
			var unscheduledCount int
			for _, rvrName := range []string{"rvr-diskful-1", "rvr-diskful-2", "rvr-diskful-3"} {
				updated := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: rvrName}, updated)).To(Succeed())

				if updated.Spec.NodeName != "" {
					scheduledCount++
					cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
					Expect(cond).ToNot(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				} else {
					unscheduledCount++
					cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
					Expect(cond).ToNot(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes))
				}
			}

			Expect(scheduledCount).To(Equal(2))
			Expect(unscheduledCount).To(Equal(1))
		})
	})

	Context("Deleting Replica Node Occupancy", func() {
		It("does not schedule new replica on node with deleting replica", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			deletingTime := metav1.Now()
			deletingRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rvr-deleting",
					DeletionTimestamp: &deletingTime,
					Finalizers:        []string{"test-finalizer"},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-a1",
				},
			}

			newRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-new"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, deletingRvr, newRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-new"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-a2"))
			Expect(updated.Spec.NodeName).ToNot(Equal("node-a1"))
		})
	})

	Context("RVR with DeletionTimestamp", func() {
		It("does not schedule RVR that is being deleted", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			deletingTime := metav1.Now()
			deletingRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rvr-deleting-unscheduled",
					DeletionTimestamp: &deletingTime,
					Finalizers:        []string{"test-finalizer"},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, deletingRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-deleting-unscheduled"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty())
		})
	})

	Context("Scheduled Condition Management", func() {
		It("sets Scheduled=True for all scheduled replicas including existing ones", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			existingRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-existing"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-a1",
				},
			}

			newRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-new"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, existingRvr, newRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updatedExisting := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-existing"}, updatedExisting)).To(Succeed())
			condExisting := meta.FindStatusCondition(updatedExisting.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(condExisting).ToNot(BeNil())
			Expect(condExisting.Status).To(Equal(metav1.ConditionTrue))
			Expect(condExisting.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonReplicaScheduled))

			updatedNew := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-new"}, updatedNew)).To(Succeed())
			Expect(updatedNew.Spec.NodeName).To(Equal("node-a2"))
			condNew := meta.FindStatusCondition(updatedNew.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(condNew).ToNot(BeNil())
			Expect(condNew.Status).To(Equal(metav1.ConditionTrue))
			Expect(condNew.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonReplicaScheduled))
		})
	})

	Context("RV Not Ready", func() {
		It("sets Scheduled=False when RV has no finalizer", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, _ := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-test",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{"vg-node-a1": 100}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty())
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending))
		})
	})

	Context("Constraint Violation Conditions", func() {
		It("sets Scheduled=False with appropriate reason when topology constraints fail", func(ctx SpecContext) {
			cluster := clusterConfigs["medium-2z"]
			eligible, scores := generateEligibleNodes(cluster)

			zoneAOnlyEligible := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0)
			zoneAOnlyScores := make(map[string]int)
			for _, node := range eligible {
				if node.ZoneName == "zone-a" {
					zoneAOnlyEligible = append(zoneAOnlyEligible, node)
					for _, lvg := range node.LVMVolumeGroups {
						if score, ok := scores[lvg.Name]; ok {
							zoneAOnlyScores[lvg.Name] = score
						}
					}
				}
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "TransZonal", cluster.RSCZones, zoneAOnlyEligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha1.ReplicatedVolumeCondIOReadyType,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}
			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr1, rvr2}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: zoneAOnlyScores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			for _, rvrName := range []string{"rvr-diskful-1", "rvr-diskful-2"} {
				updated := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: rvrName}, updated)).To(Succeed())

				if updated.Spec.NodeName == "" {
					cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
					Expect(cond).ToNot(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Or(
						Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes),
						Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonTopologyConstraintsFailed),
					))
				}
			}
		})
	})

	Context("Multi-LVG Selection", func() {
		// Helper to create eligible nodes with multiple LVGs
		createMultiLVGEligibleNodes := func(nodeConfigs []struct {
			Name     string
			Zone     string
			LVGNames []string
		}) []v1alpha1.ReplicatedStoragePoolEligibleNode {
			var eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
			for _, nc := range nodeConfigs {
				var lvgs []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
				for _, lvgName := range nc.LVGNames {
					lvgs = append(lvgs, v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						Name:  lvgName,
						Ready: true,
					})
				}
				eligible = append(eligible, v1alpha1.ReplicatedStoragePoolEligibleNode{
					NodeName:        nc.Name,
					ZoneName:        nc.Zone,
					NodeReady:       true,
					AgentReady:      true,
					LVMVolumeGroups: lvgs,
				})
			}
			return eligible
		}

		It("should prefer node with more LVGs when BestScore is equal", func(ctx SpecContext) {
			// Node-1 has 2 LVGs (lvg-1a=9, lvg-1b=5), Node-2 has 1 LVG (lvg-2a=9)
			// BestScore is equal (9), but Node-1 has more LVGs (2 > 1), so Node-1 wins.
			eligible := createMultiLVGEligibleNodes([]struct {
				Name     string
				Zone     string
				LVGNames []string
			}{
				{Name: "node-1", Zone: "zone-a", LVGNames: []string{"lvg-1a", "lvg-1b"}},
				{Name: "node-2", Zone: "zone-a", LVGNames: []string{"lvg-2a"}},
			})
			scores := map[string]int{
				"lvg-1a": 9,
				"lvg-1b": 5,
				"lvg-2a": 9,
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-1"), "Node with more LVGs should be selected")
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("lvg-1a"), "Best LVG on the node should be selected")
		})

		It("should prefer node with higher SumScore when BestScore and LVGCount are equal", func(ctx SpecContext) {
			// Node-1 has 2 LVGs (lvg-1a=9, lvg-1b=5), Node-2 has 2 LVGs (lvg-2a=9, lvg-2b=2)
			// BestScore is equal (9), LVGCount is equal (2), but Sum(14) > Sum(11), so Node-1 wins.
			eligible := createMultiLVGEligibleNodes([]struct {
				Name     string
				Zone     string
				LVGNames []string
			}{
				{Name: "node-1", Zone: "zone-a", LVGNames: []string{"lvg-1a", "lvg-1b"}},
				{Name: "node-2", Zone: "zone-a", LVGNames: []string{"lvg-2a", "lvg-2b"}},
			})
			scores := map[string]int{
				"lvg-1a": 9,
				"lvg-1b": 5,
				"lvg-2a": 9,
				"lvg-2b": 2,
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-1"), "Node with higher SumScore should be selected")
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("lvg-1a"), "Best LVG on the node should be selected")
		})

		It("should prefer node with higher BestScore regardless of LVGCount", func(ctx SpecContext) {
			// Node-1 has 2 LVGs (lvg-1a=9, lvg-1b=5), Node-2 has 1 LVG (lvg-2a=10)
			// Node-2 has higher BestScore (10 > 9), so Node-2 wins despite having fewer LVGs.
			eligible := createMultiLVGEligibleNodes([]struct {
				Name     string
				Zone     string
				LVGNames []string
			}{
				{Name: "node-1", Zone: "zone-a", LVGNames: []string{"lvg-1a", "lvg-1b"}},
				{Name: "node-2", Zone: "zone-a", LVGNames: []string{"lvg-2a"}},
			})
			scores := map[string]int{
				"lvg-1a": 9,
				"lvg-1b": 5,
				"lvg-2a": 10,
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-2"), "Node with higher BestScore should be selected")
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("lvg-2a"), "Best LVG on the node should be selected")
		})

		It("should skip LVGs without capacity (not in scores)", func(ctx SpecContext) {
			// Node-1 has 3 LVGs but only 2 have capacity, Node-2 has 1 LVG with capacity
			// After filtering: Node-1 has 2 LVGs, Node-2 has 1 LVG
			// BestScore is equal (9), but Node-1 has more suitable LVGs (2 > 1)
			eligible := createMultiLVGEligibleNodes([]struct {
				Name     string
				Zone     string
				LVGNames []string
			}{
				{Name: "node-1", Zone: "zone-a", LVGNames: []string{"lvg-1a", "lvg-1b", "lvg-1c"}},
				{Name: "node-2", Zone: "zone-a", LVGNames: []string{"lvg-2a"}},
			})
			scores := map[string]int{
				"lvg-1a": 9,
				"lvg-1b": 5,
				// lvg-1c has no capacity (not in scores)
				"lvg-2a": 9,
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-1"), "Node with more suitable LVGs should be selected")
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("lvg-1a"), "Best LVG on the node should be selected")
		})

		It("should select best LVG from the winning node", func(ctx SpecContext) {
			// Node-1 has 3 LVGs with different scores
			// Should select lvg-1b which has the highest score (15)
			eligible := createMultiLVGEligibleNodes([]struct {
				Name     string
				Zone     string
				LVGNames []string
			}{
				{Name: "node-1", Zone: "zone-a", LVGNames: []string{"lvg-1a", "lvg-1b", "lvg-1c"}},
			})
			scores := map[string]int{
				"lvg-1a": 10,
				"lvg-1b": 15,
				"lvg-1c": 5,
			}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligible)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			objects := []runtime.Object{rv, rsc, rsp, rvr}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-1"))
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("lvg-1b"), "LVG with highest score should be selected")
		})
	})

	Context("RV Validation Errors", func() {
		It("sets Scheduled=False when RV has zero size", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, _ := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("0"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{"vg-node-a1": 100}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty())
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending))
		})

		It("sets Scheduled=False when RV has empty ReplicatedStorageClassName", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, _ := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{"vg-node-a1": 100}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty())
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending))
		})
	})

	Context("RSC/RSP Not Found", func() {
		It("returns error when RSC not found", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-nonexistent",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ReplicatedStorageClass"))
		})

		It("returns error when RSP not found", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Topology: "Ignored",
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName: "rsp-nonexistent",
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ReplicatedStoragePool"))
		})

		It("sets Scheduled=False when RSC has no storage pool configured", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Topology: "Ignored",
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName: "",
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).To(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending))
		})

		It("returns Done when RV not found", func(ctx SpecContext) {
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-nonexistent"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		})
	})

	Context("Node and LVG Readiness", func() {
		It("skips nodes with NodeReady=false", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-ready",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-ready", Ready: true},
					},
				},
				{
					NodeName:   "node-not-ready",
					ZoneName:   "zone-a",
					NodeReady:  false,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-not-ready", Ready: true},
					},
				},
			}
			scores := map[string]int{"vg-ready": 50, "vg-not-ready": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-ready"), "Should skip node with NodeReady=false")
		})

		It("skips nodes with AgentReady=false", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-agent-ready",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-agent-ready", Ready: true},
					},
				},
				{
					NodeName:   "node-agent-not-ready",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: false,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-agent-not-ready", Ready: true},
					},
				},
			}
			scores := map[string]int{"vg-agent-ready": 50, "vg-agent-not-ready": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-agent-ready"), "Should skip node with AgentReady=false")
		})

		It("skips nodes with Unschedulable=true", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:      "node-schedulable",
					ZoneName:      "zone-a",
					NodeReady:     true,
					AgentReady:    true,
					Unschedulable: false,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-schedulable", Ready: true},
					},
				},
				{
					NodeName:      "node-unschedulable",
					ZoneName:      "zone-a",
					NodeReady:     true,
					AgentReady:    true,
					Unschedulable: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-unschedulable", Ready: true},
					},
				},
			}
			scores := map[string]int{"vg-schedulable": 50, "vg-unschedulable": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-schedulable"), "Should skip node with Unschedulable=true")
		})

		It("skips LVGs with Ready=false", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-1",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-ready", Ready: true},
						{Name: "vg-not-ready", Ready: false},
					},
				},
			}
			// vg-not-ready has higher score but should be skipped
			scores := map[string]int{"vg-ready": 50, "vg-not-ready": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-ready"), "Should skip LVG with Ready=false")
		})

		It("skips LVGs with Unschedulable=true", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-1",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-schedulable", Ready: true, Unschedulable: false},
						{Name: "vg-unschedulable", Ready: true, Unschedulable: true},
					},
				},
			}
			// vg-unschedulable has higher score but should be skipped
			scores := map[string]int{"vg-schedulable": 50, "vg-unschedulable": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-schedulable"), "Should skip LVG with Unschedulable=true")
		})
	})

	Context("LVMThin Storage Type", func() {
		It("sets ThinPoolName on RVR for LVMThin storage", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-1",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-thin", Ready: true, ThinPoolName: "thin-pool-1"},
					},
				},
			}
			scores := map[string]int{"vg-thin": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-thin"))
			Expect(updated.Spec.LVMVolumeGroupThinPoolName).To(Equal("thin-pool-1"), "ThinPoolName should be set for LVMThin")
		})

		It("does not set ThinPoolName on RVR for LVM (thick) storage", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-1",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-thick", Ready: true, ThinPoolName: ""},
					},
				},
			}
			scores := map[string]int{"vg-thick": 100}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-thick"))
			Expect(updated.Spec.LVMVolumeGroupThinPoolName).To(BeEmpty(), "ThinPoolName should be empty for LVM thick")
		})
	})

	Context("Requeue Behavior", func() {
		It("returns RequeueAfter(30s) on scheduling failure (no suitable nodes)", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, _ := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			// Return empty scores - no nodes have capacity
			extender := &mockExtenderClient{scores: map[string]int{}}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred(), "Scheduling failure should not return error")
			Expect(result.RequeueAfter).To(Equal(30*time.Second), "Should requeue after 30s for scheduling failure")
		})
	})

	Context("Access Replica Handling", func() {
		It("ignores Access replicas (does not schedule them)", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			accessRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeAccess,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, accessRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(BeEmpty(), "Access replica should not be scheduled")
		})

		It("Access replicas occupy nodes for other replicas", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			// Access replica already on node-a1 - this DOES block it
			accessRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeAccess,
					NodeName:             "node-a1",
				},
			}

			diskfulRvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, accessRvr, diskfulRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			// node-a1 is occupied by Access replica, so Diskful goes to node-a2
			Expect(updated.Spec.NodeName).To(Equal("node-a2"), "Access replica occupies node, preventing other replicas")
		})
	})

	Context("Label Management", func() {
		It("sets NodeNameLabelKey on newly scheduled RVR", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Labels[v1alpha1.NodeNameLabelKey]).To(Equal(updated.Spec.NodeName), "NodeNameLabelKey should match scheduled node")
		})

		It("adds NodeNameLabelKey to already scheduled RVR without label", func(ctx SpecContext) {
			cluster := clusterConfigs["small-1z"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}

			// Already scheduled but without the label
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "rvr-diskful-1",
					Labels: map[string]string{},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-a1",
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Labels[v1alpha1.NodeNameLabelKey]).To(Equal("node-a1"), "NodeNameLabelKey should be added for already scheduled RVR")
		})
	})

	Context("Topology Edge Cases", func() {
		It("TransZonal: round-robin for 4+ replicas across 3 zones", func(ctx SpecContext) {
			cluster := clusterConfigs["large-3z-3n"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "TransZonal", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha1.ReplicatedVolumeCondIOReadyType,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			// Create 4 Diskful replicas to schedule
			var rvrList []*v1alpha1.ReplicatedVolumeReplica
			for i := 1; i <= 4; i++ {
				rvrList = append(rvrList, &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-diskful-%d", i)},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-test",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				})
			}

			objects := []runtime.Object{rv, rsc, rsp}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			// Verify all 4 replicas scheduled
			zonesUsed := make(map[string]int)
			for i := 1; i <= 4; i++ {
				updated := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-diskful-%d", i)}, updated)).To(Succeed())
				Expect(updated.Spec.NodeName).ToNot(BeEmpty(), "Replica %d should be scheduled", i)

				// Find zone for node
				for _, node := range eligibleNodes {
					if node.NodeName == updated.Spec.NodeName {
						zonesUsed[node.ZoneName]++
						break
					}
				}
			}

			// With 4 replicas across 3 zones, one zone should have 2 replicas, others have 1
			Expect(len(zonesUsed)).To(Equal(3), "All 3 zones should be used")
			maxInZone := 0
			for _, count := range zonesUsed {
				if count > maxInZone {
					maxInZone = count
				}
			}
			Expect(maxInZone).To(Equal(2), "Max replicas in any zone should be 2 (round-robin)")
		})

		It("Zonal: zone selection is sticky for subsequent replicas", func(ctx SpecContext) {
			cluster := clusterConfigs["medium-2z-4n"]
			eligibleNodes, scores := generateEligibleNodes(cluster)

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Zonal", cluster.RSCZones, eligibleNodes)
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha1.ReplicatedVolumeCondIOReadyType,
						Status: metav1.ConditionTrue,
					}},
				},
			}

			// Create 3 Diskful replicas
			var rvrList []*v1alpha1.ReplicatedVolumeReplica
			for i := 1; i <= 3; i++ {
				rvrList = append(rvrList, &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-diskful-%d", i)},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-test",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				})
			}

			objects := []runtime.Object{rv, rsc, rsp}
			for _, rvr := range rvrList {
				objects = append(objects, rvr)
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, extender)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			// Verify all replicas are in the same zone
			var scheduledZone string
			for i := 1; i <= 3; i++ {
				updated := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-diskful-%d", i)}, updated)).To(Succeed())
				Expect(updated.Spec.NodeName).ToNot(BeEmpty(), "Replica %d should be scheduled", i)

				// Find zone for node
				for _, node := range eligibleNodes {
					if node.NodeName == updated.Spec.NodeName {
						if scheduledZone == "" {
							scheduledZone = node.ZoneName
						} else {
							Expect(node.ZoneName).To(Equal(scheduledZone), "All replicas should be in the same zone for Zonal topology")
						}
						break
					}
				}
			}
		})
	})

	Context("AttachTo Bonus", func() {
		It("prefers attachTo nodes with score bonus", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-attachto",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-attachto", Ready: true},
					},
				},
				{
					NodeName:   "node-other",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-other", Ready: true},
					},
				},
			}
			// node-other has slightly higher score, but attachTo bonus should tip the balance
			scores := map[string]int{"vg-attachto": 50, "vg-other": 60}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DesiredAttachTo: []string{"node-attachto"},
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-attachto"), "AttachTo node should be preferred due to bonus")
		})

		It("attachTo bonus does not override significantly higher capacity score", func(ctx SpecContext) {
			eligible := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName:   "node-attachto",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-attachto", Ready: true},
					},
				},
				{
					NodeName:   "node-other",
					ZoneName:   "zone-a",
					NodeReady:  true,
					AgentReady: true,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-other", Ready: true},
					},
				},
			}
			// node-other has much higher score (2000 > 50 + 1000 bonus)
			scores := map[string]int{"vg-attachto": 50, "vg-other": 2000}

			rsc, rsp := generateRSCAndRSP("rsc-test", "rsp-test", "Ignored", nil, nil)
			rsp.Status.EligibleNodes = eligible
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-test",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DesiredAttachTo: []string{"node-attachto"},
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(rv, rsc, rsp, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconcilerWithExtender(cl, &mockExtenderClient{scores: scores})

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-diskful-1"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal("node-other"), "Node with significantly higher score should win despite attachTo bonus")
		})
	})
})
