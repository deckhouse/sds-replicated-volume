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
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller_new"
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
	ctx context.Context,
	lvgs []rvrschedulingcontroller.LVGQuery,
	volume rvrschedulingcontroller.VolumeInfo,
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

func generateEligibleNodes(setup ClusterSetup) ([]v1alpha1.ReplicatedStorageClassEligibleNode, map[string]int) {
	var eligible []v1alpha1.ReplicatedStorageClassEligibleNode
	scores := make(map[string]int)

	for _, zone := range setup.Zones {
		for i := 1; i <= setup.NodesPerZone; i++ {
			nodeName := fmt.Sprintf("node-%s%d", zone[len(zone)-1:], i)
			lvgName := fmt.Sprintf("vg-%s", nodeName)

			node := v1alpha1.ReplicatedStorageClassEligibleNode{
				NodeName: nodeName,
				ZoneName: zone,
				Ready:    true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup{
					{Name: lvgName},
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

		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				StoragePool:  "pool-1",
				VolumeAccess: "Any",
				Topology:     tc.Topology,
				Zones:        cluster.RSCZones,
			},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				EligibleNodes: eligibleNodes,
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

		objects := []runtime.Object{rv, rsc}
		for _, rvr := range rvrList {
			objects = append(objects, rvr)
		}

		cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
			WithScheme(scheme)).
			WithRuntimeObjects(objects...).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			Build()

		extender := &mockExtenderClient{scores: scores}
		rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Any",
					Topology:     "Ignored",
					Zones:        cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
					Zones:       cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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

			zoneAScores := make(map[string]int)
			for lvg, score := range scores {
				if lvg == "vg-node-a1" || lvg == "vg-node-a2" {
					zoneAScores[lvg] = score
				}
			}
			extender := &mockExtenderClient{scores: zoneAScores}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
					Zones:       cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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
				WithRuntimeObjects(rv, rsc, rvr1, rvr2, rvr3).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
					Zones:       cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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
				WithRuntimeObjects(rv, rsc, deletingRvr, newRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
					Zones:       cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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
				WithRuntimeObjects(rv, rsc, deletingRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
					Zones:       cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
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
				WithRuntimeObjects(rv, rsc, existingRvr, newRvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: scores}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "pool-1",
					Topology:    "Ignored",
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: eligibleNodes,
				},
			}
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
				WithRuntimeObjects(rv, rsc, rvr).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()

			extender := &mockExtenderClient{scores: map[string]int{"vg-node-a1": 100}}
			rec := rvrschedulingcontroller.NewReconciler(cl, extender)

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

			zoneAOnlyEligible := make([]v1alpha1.ReplicatedStorageClassEligibleNode, 0)
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

			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool:  "pool-1",
					VolumeAccess: "Any",
					Topology:     "TransZonal",
					Zones:        cluster.RSCZones,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					EligibleNodes: zoneAOnlyEligible,
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

			objects := []runtime.Object{rv, rsc, rvr1, rvr2}
			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
				WithScheme(scheme)).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
				Build()
			rec := rvrschedulingcontroller.NewReconciler(cl, &mockExtenderClient{scores: zoneAOnlyScores})

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
})
