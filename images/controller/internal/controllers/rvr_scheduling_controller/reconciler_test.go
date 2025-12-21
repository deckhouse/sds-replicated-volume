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

package rvr_scheduling_controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"

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

// ClusterSetup defines a cluster configuration for tests
type ClusterSetup struct {
	Name         string
	Zones        []string       // zones in cluster
	RSCZones     []string       // zones in RSC (can be less than cluster zones)
	NodesPerZone int            // nodes per zone
	NodeScores   map[string]int // node -> score from scheduler extender
}

// ExistingReplica represents an already scheduled replica
type ExistingReplica struct {
	Type     string // Diskful, Access, TieBreaker
	NodeName string
}

// ReplicasToSchedule defines how many replicas of each type need to be scheduled
type ReplicasToSchedule struct {
	Diskful    int
	TieBreaker int
}

// ExpectedResult defines the expected outcome of a test
type ExpectedResult struct {
	Error           string   // expected error substring (empty if success)
	DiskfulZones    []string // zones where Diskful replicas should be (nil = any)
	TieBreakerZones []string // zones where TieBreaker replicas should be (nil = any)
	DiskfulNodes    []string // specific nodes for Diskful (nil = check zones only)
	TieBreakerNodes []string // specific nodes for TieBreaker (nil = check zones only)
}

// IntegrationTestCase defines a full integration test case
type IntegrationTestCase struct {
	Name       string
	Cluster    string // reference to ClusterSetup.Name
	Topology   string // Zonal, TransZonal, Ignored
	PublishOn  []string
	Existing   []ExistingReplica
	ToSchedule ReplicasToSchedule
	Expected   ExpectedResult
}

// generateNodes creates nodes for a cluster setup
func generateNodes(setup ClusterSetup) ([]*corev1.Node, map[string]int) {
	var nodes []*corev1.Node
	scores := make(map[string]int)

	for _, zone := range setup.Zones {
		for i := 1; i <= setup.NodesPerZone; i++ {
			nodeName := fmt.Sprintf("node-%s%d", zone[len(zone)-1:], i) // e.g., node-a1, node-a2
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: map[string]string{"topology.kubernetes.io/zone": zone},
				},
			}
			nodes = append(nodes, node)

			// Use predefined score or generate based on position
			if score, ok := setup.NodeScores[nodeName]; ok {
				scores[nodeName] = score
			} else {
				// Default: first node in first zone gets highest score
				scores[nodeName] = 100 - (len(nodes)-1)*10
			}
		}
	}
	return nodes, scores
}

// generateLVGs creates LVMVolumeGroups for nodes
func generateLVGs(nodes []*corev1.Node) ([]*snc.LVMVolumeGroup, *v1alpha1.ReplicatedStoragePool) {
	var lvgs []*snc.LVMVolumeGroup
	var lvgRefs []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups

	for _, node := range nodes {
		lvgName := fmt.Sprintf("vg-%s", node.Name)
		lvg := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: lvgName},
			Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: node.Name}}},
		}
		lvgs = append(lvgs, lvg)
		lvgRefs = append(lvgRefs, v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: lvgName})
	}

	rsp := &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type:            "LVM",
			LVMVolumeGroups: lvgRefs,
		},
	}

	return lvgs, rsp
}

// createMockServer creates a mock scheduler extender server
func createMockServer(scores map[string]int, lvgToNode map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			LVGS []struct{ Name string } `json:"lvgs"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		resp := map[string]any{"lvgs": []map[string]any{}}
		for _, lvg := range req.LVGS {
			nodeName := lvgToNode[lvg.Name]
			score := scores[nodeName]
			if score == 0 {
				score = 50 // default score
			}
			resp["lvgs"] = append(resp["lvgs"].([]map[string]any), map[string]any{"name": lvg.Name, "score": score})
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// Cluster configurations
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
		RSCZones:     []string{"zone-a", "zone-b", "zone-c"}, // zone-d NOT in RSC!
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
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(snc.AddToScheme(scheme))
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
		utilruntime.Must(v1alpha3.AddToScheme(scheme))
	})

	// Helper to run a test case
	runTestCase := func(ctx context.Context, tc IntegrationTestCase) {
		cluster := clusterConfigs[tc.Cluster]
		Expect(cluster.Name).ToNot(BeEmpty(), "Unknown cluster: %s", tc.Cluster)

		// Generate cluster resources
		nodes, scores := generateNodes(cluster)
		lvgs, rsp := generateLVGs(nodes)

		// Build lvg -> node mapping for mock server
		lvgToNode := make(map[string]string)
		for _, lvg := range lvgs {
			if len(lvg.Status.Nodes) > 0 {
				lvgToNode[lvg.Name] = lvg.Status.Nodes[0].Name
			}
		}

		// Create mock server
		mockServer := createMockServer(scores, lvgToNode)
		defer mockServer.Close()
		os.Setenv("SCHEDULER_EXTENDER_URL", mockServer.URL)
		defer os.Unsetenv("SCHEDULER_EXTENDER_URL")

		// Create RSC
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-test"},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				StoragePool:  "pool-1",
				VolumeAccess: "Any",
				Topology:     tc.Topology,
				Zones:        cluster.RSCZones,
			},
		}

		// Create RV
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-test",
				Finalizers: []string{v1alpha3.ControllerAppFinalizer},
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-test",
				PublishOn:                  tc.PublishOn,
			},
			Status: &v1alpha3.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{{
					Type:   v1alpha3.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				}},
			},
		}

		// Create RVRs
		var rvrList []*v1alpha3.ReplicatedVolumeReplica
		rvrIndex := 1

		// Existing replicas (already scheduled)
		for _, existing := range tc.Existing {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-existing-%d", rvrIndex)},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 existing.Type,
					NodeName:             existing.NodeName,
				},
			}
			rvrList = append(rvrList, rvr)
			rvrIndex++
		}

		// Diskful replicas to schedule
		for i := 0; i < tc.ToSchedule.Diskful; i++ {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-diskful-%d", i+1)},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha3.ReplicaTypeDiskful,
				},
			}
			rvrList = append(rvrList, rvr)
		}

		// TieBreaker replicas to schedule
		for i := 0; i < tc.ToSchedule.TieBreaker; i++ {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rvr-tiebreaker-%d", i+1)},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-test",
					Type:                 v1alpha3.ReplicaTypeTieBreaker,
				},
			}
			rvrList = append(rvrList, rvr)
		}

		// Build objects list
		objects := []runtime.Object{rv, rsc, rsp}
		for _, node := range nodes {
			objects = append(objects, node)
		}
		for _, lvg := range lvgs {
			objects = append(objects, lvg)
		}
		for _, rvr := range rvrList {
			objects = append(objects, rvr)
		}

		// Create client and reconciler
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(objects...).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
			Build()
		rec, err := rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
		Expect(err).ToNot(HaveOccurred())

		// Reconcile
		_, err = rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})

		// Check result
		if tc.Expected.Error != "" {
			Expect(err).To(HaveOccurred(), "Expected error but got none")
			Expect(err.Error()).To(ContainSubstring(tc.Expected.Error), "Error message mismatch")
			return
		}

		Expect(err).ToNot(HaveOccurred(), "Unexpected error: %v", err)

		// Verify Diskful replicas
		var scheduledDiskful []string
		var diskfulZones []string
		for i := 0; i < tc.ToSchedule.Diskful; i++ {
			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-diskful-%d", i+1)}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).ToNot(BeEmpty(), "Diskful replica %d not scheduled", i+1)
			scheduledDiskful = append(scheduledDiskful, updated.Spec.NodeName)

			// Find zone for this node
			for _, node := range nodes {
				if node.Name == updated.Spec.NodeName {
					zone := node.Labels["topology.kubernetes.io/zone"]
					if !slices.Contains(diskfulZones, zone) {
						diskfulZones = append(diskfulZones, zone)
					}
					break
				}
			}
		}

		// Check Diskful zones
		if tc.Expected.DiskfulZones != nil {
			Expect(diskfulZones).To(ConsistOf(tc.Expected.DiskfulZones), "Diskful zones mismatch")
		}

		// Check Diskful nodes
		if tc.Expected.DiskfulNodes != nil {
			Expect(scheduledDiskful).To(ConsistOf(tc.Expected.DiskfulNodes), "Diskful nodes mismatch")
		}

		// Verify TieBreaker replicas
		var scheduledTieBreaker []string
		var tieBreakerZones []string
		for i := 0; i < tc.ToSchedule.TieBreaker; i++ {
			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("rvr-tiebreaker-%d", i+1)}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).ToNot(BeEmpty(), "TieBreaker replica %d not scheduled", i+1)
			scheduledTieBreaker = append(scheduledTieBreaker, updated.Spec.NodeName)

			// Find zone for this node
			for _, node := range nodes {
				if node.Name == updated.Spec.NodeName {
					zone := node.Labels["topology.kubernetes.io/zone"]
					if !slices.Contains(tieBreakerZones, zone) {
						tieBreakerZones = append(tieBreakerZones, zone)
					}
					break
				}
			}
		}

		// Check TieBreaker zones
		if tc.Expected.TieBreakerZones != nil {
			Expect(tieBreakerZones).To(ConsistOf(tc.Expected.TieBreakerZones), "TieBreaker zones mismatch")
		}

		// Check TieBreaker nodes
		if tc.Expected.TieBreakerNodes != nil {
			Expect(scheduledTieBreaker).To(ConsistOf(tc.Expected.TieBreakerNodes), "TieBreaker nodes mismatch")
		}

		// Verify no node has multiple replicas
		allScheduled := append(scheduledDiskful, scheduledTieBreaker...)
		// Add existing replica nodes
		for _, existing := range tc.Existing {
			allScheduled = append(allScheduled, existing.NodeName)
		}
		nodeCount := make(map[string]int)
		for _, node := range allScheduled {
			nodeCount[node]++
			Expect(nodeCount[node]).To(Equal(1), "Node %s has multiple replicas", node)
		}
	}

	// ==================== ZONAL TOPOLOGY ====================
	Context("Zonal Topology", func() {
		zonalTestCases := []IntegrationTestCase{
			{
				Name:       "1. small-1z: D:2, TB:1 - all in zone-a",
				Cluster:    "small-1z",
				Topology:   "Zonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}},
			},
			{
				Name:       "2. small-1z: publishOn node-a1 - D on node-a1",
				Cluster:    "small-1z",
				Topology:   "Zonal",
				PublishOn:  []string{"node-a1"},
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulNodes: []string{"node-a1"}, TieBreakerNodes: []string{"node-a2"}},
			},
			{
				Name:       "3. medium-2z: publishOn same zone - all in zone-a",
				Cluster:    "medium-2z",
				Topology:   "Zonal",
				PublishOn:  []string{"node-a1", "node-a2"},
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}},
			},
			{
				Name:       "4. medium-2z: publishOn different zones - pick one zone",
				Cluster:    "medium-2z",
				Topology:   "Zonal",
				PublishOn:  []string{"node-a1", "node-b1"},
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{}, // any zone is ok
			},
			{
				Name:       "5. medium-2z-4n: existing D in zone-a - new D and TB in zone-a",
				Cluster:    "medium-2z-4n",
				Topology:   "Zonal",
				PublishOn:  nil,
				Existing:   []ExistingReplica{{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"}},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}, TieBreakerZones: []string{"zone-a"}},
			},
			{
				Name:      "6. medium-2z: existing D in different zones - topology conflict",
				Cluster:   "medium-2z",
				Topology:  "Zonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{Error: "multiple zones"},
			},
			{
				Name:       "7. large-3z: no publishOn - pick best zone by score",
				Cluster:    "large-3z",
				Topology:   "Zonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 0},
				Expected:   ExpectedResult{}, // any zone, best score wins
			},
			{
				Name:       "8. xlarge-4z: publishOn zone-d (not in RSC) - D in zone-d (targetZones priority)",
				Cluster:    "xlarge-4z",
				Topology:   "Zonal",
				PublishOn:  []string{"node-d1"},
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-d"}, TieBreakerZones: []string{"zone-d"}},
			},
			{
				Name:      "9. small-1z: all nodes occupied - no candidate nodes",
				Cluster:   "small-1z",
				Topology:  "Zonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{Error: "no candidate nodes"},
			},
			{
				Name:       "10. medium-2z: TB only without Diskful - error",
				Cluster:    "medium-2z",
				Topology:   "Zonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{Error: "no Diskful replicas"},
			},
			{
				Name:      "11. medium-2z-4n: existing D+TB in zone-a - new D in zone-a",
				Cluster:   "medium-2z-4n",
				Topology:  "Zonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeTieBreaker, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a"}},
			},
			{
				Name:      "12. medium-2z-4n: existing D+Access in zone-a - new TB in zone-a",
				Cluster:   "medium-2z-4n",
				Topology:  "Zonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeAccess, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{TieBreakerZones: []string{"zone-a"}},
			},
		}

		for _, tc := range zonalTestCases {
			tc := tc // capture
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})

	// ==================== TRANSZONAL TOPOLOGY ====================
	Context("TransZonal Topology", func() {
		transZonalTestCases := []IntegrationTestCase{
			{
				Name:       "1. large-3z: D:3 - one per zone",
				Cluster:    "large-3z",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 3, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a", "zone-b", "zone-c"}},
			},
			{
				Name:       "2. large-3z: D:2, TB:1 - D in 2 zones, TB in 3rd",
				Cluster:    "large-3z",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 1},
				Expected:   ExpectedResult{}, // distribution across 3 zones
			},
			{
				Name:      "3. large-3z: existing D in zone-a,b - new D in zone-c",
				Cluster:   "large-3z",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-c"}},
			},
			{
				Name:      "4. large-3z: existing D in zone-a,b - TB in zone-c",
				Cluster:   "large-3z",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{TieBreakerZones: []string{"zone-c"}},
			},
			{
				Name:       "5. medium-2z: existing D in zone-a - new D in zone-b",
				Cluster:    "medium-2z",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   []ExistingReplica{{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"}},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-b"}},
			},
			{
				Name:      "6. medium-2z: zones full, new D - cannot guarantee even",
				Cluster:   "medium-2z",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{}, // will place in any zone with free node
			},
			{
				Name:       "7. xlarge-4z: D:3, TB:1 - D in RSC zones only",
				Cluster:    "xlarge-4z",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 3, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-a", "zone-b", "zone-c"}},
			},
			{
				Name:       "8. large-3z-3n: D:5, TB:1 - distribution 2-2-1",
				Cluster:    "large-3z-3n",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 5, TieBreaker: 1},
				Expected:   ExpectedResult{}, // 2-2-1 distribution + 1 TB
			},
			{
				Name:      "9. medium-2z: all nodes occupied - no candidate nodes",
				Cluster:   "medium-2z",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a2"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{Error: "no candidate nodes"},
			},
			{
				Name:       "10. large-3z: TB only, no existing - TB in any zone",
				Cluster:    "large-3z",
				Topology:   "TransZonal",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{}, // any zone ok (all have 0 replicas)
			},
			{
				Name:      "11. large-3z-3n: existing D+TB in zone-a,b - new D in zone-c",
				Cluster:   "large-3z-3n",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeTieBreaker, NodeName: "node-a2"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{DiskfulZones: []string{"zone-c"}},
			},
			{
				Name:      "12. large-3z-3n: existing D+Access across zones - new TB balances",
				Cluster:   "large-3z-3n",
				Topology:  "TransZonal",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeAccess, NodeName: "node-a2"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{TieBreakerZones: []string{"zone-c"}}, // zone-c has 0 replicas
			},
		}

		for _, tc := range transZonalTestCases {
			tc := tc // capture
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})

	// ==================== IGNORED TOPOLOGY ====================
	Context("Ignored Topology", func() {
		ignoredTestCases := []IntegrationTestCase{
			{
				Name:       "1. large-3z: D:2, TB:1 - best score wins",
				Cluster:    "large-3z",
				Topology:   "Ignored",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 1},
				Expected:   ExpectedResult{}, // any nodes, best score
			},
			{
				Name:       "2. medium-2z: publishOn - prefer publishOn nodes",
				Cluster:    "medium-2z",
				Topology:   "Ignored",
				PublishOn:  []string{"node-a1", "node-b1"},
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 1},
				Expected:   ExpectedResult{DiskfulNodes: []string{"node-a1", "node-b1"}},
			},
			{
				Name:       "3. small-1z-4n: D:2, TB:2 - 4 replicas on 4 nodes",
				Cluster:    "small-1z-4n",
				Topology:   "Ignored",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 2, TieBreaker: 2},
				Expected:   ExpectedResult{}, // all 4 nodes used
			},
			{
				Name:       "4. xlarge-4z: D:3, TB:1 - any 4 nodes by score",
				Cluster:    "xlarge-4z",
				Topology:   "Ignored",
				PublishOn:  nil,
				Existing:   nil,
				ToSchedule: ReplicasToSchedule{Diskful: 3, TieBreaker: 1},
				Expected:   ExpectedResult{}, // best 4 nodes
			},
			{
				Name:      "5. small-1z: all nodes occupied - no candidate nodes",
				Cluster:   "small-1z",
				Topology:  "Ignored",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{Error: "no candidate nodes"},
			},
			{
				Name:      "6. small-1z-4n: existing D+TB - new D on best remaining",
				Cluster:   "small-1z-4n",
				Topology:  "Ignored",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeTieBreaker, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 0},
				Expected:   ExpectedResult{}, // any of remaining nodes
			},
			{
				Name:      "7. small-1z-4n: existing D+Access - new TB",
				Cluster:   "small-1z-4n",
				Topology:  "Ignored",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeAccess, NodeName: "node-a2"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 0, TieBreaker: 1},
				Expected:   ExpectedResult{}, // any of remaining nodes
			},
			{
				Name:      "8. medium-2z-4n: existing mixed types - new D+TB",
				Cluster:   "medium-2z-4n",
				Topology:  "Ignored",
				PublishOn: nil,
				Existing: []ExistingReplica{
					{Type: v1alpha3.ReplicaTypeDiskful, NodeName: "node-a1"},
					{Type: v1alpha3.ReplicaTypeAccess, NodeName: "node-a2"},
					{Type: v1alpha3.ReplicaTypeTieBreaker, NodeName: "node-b1"},
				},
				ToSchedule: ReplicasToSchedule{Diskful: 1, TieBreaker: 1},
				Expected:   ExpectedResult{}, // best remaining nodes by score
			},
		}

		for _, tc := range ignoredTestCases {
			tc := tc // capture
			It(tc.Name, func(ctx SpecContext) {
				runTestCase(ctx, tc)
			})
		}
	})
})

// ==================== ACCESS PHASE TESTS (kept separate) ====================
var _ = Describe("Access Phase Tests", Ordered, func() {
	var (
		scheme     *runtime.Scheme
		cl         client.WithWatch
		rec        *rvrschedulingcontroller.Reconciler
		mockServer *httptest.Server
	)

	BeforeEach(func() {
		mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				LVGS []struct{ Name string } `json:"lvgs"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			resp := map[string]any{"lvgs": []map[string]any{}}
			for _, lvg := range req.LVGS {
				resp["lvgs"] = append(resp["lvgs"].([]map[string]any), map[string]any{"name": lvg.Name, "score": 100})
			}
			json.NewEncoder(w).Encode(resp)
		}))
		os.Setenv("SCHEDULER_EXTENDER_URL", mockServer.URL)
		scheme = runtime.NewScheme()
		utilruntime.Must(corev1.AddToScheme(scheme))
		utilruntime.Must(snc.AddToScheme(scheme))
		utilruntime.Must(v1alpha1.AddToScheme(scheme))
		utilruntime.Must(v1alpha3.AddToScheme(scheme))
	})

	AfterEach(func() {
		os.Unsetenv("SCHEDULER_EXTENDER_URL")
		mockServer.Close()
	})

	var (
		rv                    *v1alpha3.ReplicatedVolume
		rsc                   *v1alpha1.ReplicatedStorageClass
		rsp                   *v1alpha1.ReplicatedStoragePool
		lvgA                  *snc.LVMVolumeGroup
		lvgB                  *snc.LVMVolumeGroup
		nodeA                 *corev1.Node
		nodeB                 *corev1.Node
		rvrList               []*v1alpha3.ReplicatedVolumeReplica
		withStatusSubresource bool
	)

	BeforeEach(func() {
		rv = &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-access",
				Finalizers: []string{v1alpha3.ControllerAppFinalizer},
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-access",
				PublishOn:                  []string{"node-a", "node-b"},
			},
			Status: &v1alpha3.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{{
					Type:   v1alpha3.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				}},
			},
		}

		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-access"},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				StoragePool:  "pool-access",
				VolumeAccess: "Any",
				Topology:     "Ignored",
			},
		}

		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-access"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: "LVM",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "vg-a"}, {Name: "vg-b"},
				},
			},
		}

		lvgA = &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vg-a"},
			Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-a"}}},
		}
		lvgB = &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vg-b"},
			Status:     snc.LVMVolumeGroupStatus{Nodes: []snc.LVMVolumeGroupNode{{Name: "node-b"}}},
		}

		nodeA = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-a",
				Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
			},
		}
		nodeB = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-b",
				Labels: map[string]string{"topology.kubernetes.io/zone": "zone-a"},
			},
		}

		rvrList = nil
		withStatusSubresource = false
	})

	JustBeforeEach(func() {
		objects := []runtime.Object{rv, rsc, rsp, lvgA, nodeA}
		if lvgB != nil {
			objects = append(objects, lvgB)
		}
		if nodeB != nil {
			objects = append(objects, nodeB)
		}
		for _, rvr := range rvrList {
			objects = append(objects, rvr)
		}
		builder := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...)
		if withStatusSubresource {
			builder = builder.WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
		}
		cl = builder.Build()
		rec, _ = rvrschedulingcontroller.NewReconciler(cl, logr.Discard(), scheme)
	})

	When("one publishOn node has diskful replica", func() {
		BeforeEach(func() {
			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-diskful"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 v1alpha3.ReplicaTypeDiskful,
						NodeName:             "node-a",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-1"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 "Access",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-2"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 "Access",
					},
				},
			}
		})

		It("schedules access replica only on free publishOn node", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-1"}, updated1)).To(Succeed())
			updated2 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-2"}, updated2)).To(Succeed())

			nodeNames := []string{updated1.Spec.NodeName, updated2.Spec.NodeName}
			Expect(nodeNames).To(ContainElement("node-b"))
			Expect(nodeNames).To(ContainElement(""))
		})
	})

	When("all publishOn nodes already have replicas", func() {
		BeforeEach(func() {
			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 v1alpha3.ReplicaTypeDiskful,
						NodeName:             "node-a",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-b"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 "Access",
						NodeName:             "node-b",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-access-unscheduled"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 "Access",
					},
				},
			}
		})

		It("does not schedule unscheduled access replica", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-access-unscheduled"}, updated)).To(Succeed())
			Expect(updated.Spec.NodeName).To(Equal(""))
		})
	})

	When("checking Scheduled condition", func() {
		BeforeEach(func() {
			rv.Spec.PublishOn = []string{"node-a", "node-b"}
			withStatusSubresource = true
			rvrList = []*v1alpha3.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-scheduled"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 v1alpha3.ReplicaTypeDiskful,
						NodeName:             "node-a",
					},
					Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-to-schedule"},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-access",
						Type:                 v1alpha3.ReplicaTypeDiskful,
					},
					Status: &v1alpha3.ReplicatedVolumeReplicaStatus{},
				},
			}
		})

		It("sets Scheduled=True for all scheduled replicas", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: rv.Name}})
			Expect(err).ToNot(HaveOccurred())

			// Check already-scheduled replica gets condition fixed
			updatedScheduled := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-scheduled"}, updatedScheduled)).To(Succeed())
			condScheduled := meta.FindStatusCondition(updatedScheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
			Expect(condScheduled).ToNot(BeNil())
			Expect(condScheduled.Status).To(Equal(metav1.ConditionTrue))
			Expect(condScheduled.Reason).To(Equal(v1alpha3.ReasonSchedulingReplicaScheduled))

			// Check newly-scheduled replica gets NodeName and Scheduled condition
			updatedNewlyScheduled := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-to-schedule"}, updatedNewlyScheduled)).To(Succeed())
			Expect(updatedNewlyScheduled.Spec.NodeName).To(Equal("node-b"))
			condNewlyScheduled := meta.FindStatusCondition(updatedNewlyScheduled.Status.Conditions, v1alpha3.ConditionTypeScheduled)
			Expect(condNewlyScheduled).ToNot(BeNil())
			Expect(condNewlyScheduled.Status).To(Equal(metav1.ConditionTrue))
			Expect(condNewlyScheduled.Reason).To(Equal(v1alpha3.ReasonSchedulingReplicaScheduled))
		})
	})
})
