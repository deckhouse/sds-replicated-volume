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

package rvrschedulingcontroller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestComputeEligibleNodeNames(t *testing.T) {
	tests := []struct {
		name     string
		eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
		occupied map[string]struct{}
		expected []string
	}{
		{
			name:     "empty input",
			eligible: nil,
			occupied: nil,
			expected: nil,
		},
		{
			name: "all nodes ready and not occupied",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: true, AgentReady: true, Unschedulable: false},
			},
			occupied: map[string]struct{}{},
			expected: []string{"node-1", "node-2"},
		},
		{
			name: "filters out node with NodeReady=false",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: false, AgentReady: true, Unschedulable: false},
			},
			occupied: map[string]struct{}{},
			expected: []string{"node-1"},
		},
		{
			name: "filters out node with AgentReady=false",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: true, AgentReady: false, Unschedulable: false},
			},
			occupied: map[string]struct{}{},
			expected: []string{"node-1"},
		},
		{
			name: "filters out node with Unschedulable=true",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: true, AgentReady: true, Unschedulable: true},
			},
			occupied: map[string]struct{}{},
			expected: []string{"node-1"},
		},
		{
			name: "filters out occupied nodes",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: true, AgentReady: true, Unschedulable: false},
			},
			occupied: map[string]struct{}{"node-1": {}},
			expected: []string{"node-2"},
		},
		{
			name: "filters out multiple conditions",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", NodeReady: true, AgentReady: true, Unschedulable: false},
				{NodeName: "node-2", NodeReady: false, AgentReady: true, Unschedulable: false},
				{NodeName: "node-3", NodeReady: true, AgentReady: false, Unschedulable: false},
				{NodeName: "node-4", NodeReady: true, AgentReady: true, Unschedulable: true},
				{NodeName: "node-5", NodeReady: true, AgentReady: true, Unschedulable: false},
			},
			occupied: map[string]struct{}{"node-5": {}},
			expected: []string{"node-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeEligibleNodeNames(tt.eligible, tt.occupied)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeReplicasByZone(t *testing.T) {
	tests := []struct {
		name        string
		replicas    []*v1alpha1.ReplicatedVolumeReplica
		replicaType v1alpha1.ReplicaType
		nodeToZone  map[string]string
		expected    map[string]int
	}{
		{
			name:        "empty input",
			replicas:    nil,
			replicaType: "",
			nodeToZone:  nil,
			expected:    map[string]int{},
		},
		{
			name: "counts all replicas when type is empty",
			replicas: []*v1alpha1.ReplicatedVolumeReplica{
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeTieBreaker, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b"}},
			},
			replicaType: "",
			nodeToZone:  map[string]string{"node-a": "zone-a", "node-b": "zone-b"},
			expected:    map[string]int{"zone-a": 2, "zone-b": 1},
		},
		{
			name: "filters by replica type",
			replicas: []*v1alpha1.ReplicatedVolumeReplica{
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeTieBreaker, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b"}},
			},
			replicaType: v1alpha1.ReplicaTypeDiskful,
			nodeToZone:  map[string]string{"node-a": "zone-a", "node-b": "zone-b"},
			expected:    map[string]int{"zone-a": 1, "zone-b": 1},
		},
		{
			name: "ignores replicas without NodeName",
			replicas: []*v1alpha1.ReplicatedVolumeReplica{
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: ""}},
			},
			replicaType: "",
			nodeToZone:  map[string]string{"node-a": "zone-a"},
			expected:    map[string]int{"zone-a": 1},
		},
		{
			name: "ignores replicas on nodes without zone",
			replicas: []*v1alpha1.ReplicatedVolumeReplica{
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-unknown"}},
			},
			replicaType: "",
			nodeToZone:  map[string]string{"node-a": "zone-a"},
			expected:    map[string]int{"zone-a": 1},
		},
		{
			name: "ignores replicas on nodes with empty zone",
			replicas: []*v1alpha1.ReplicatedVolumeReplica{
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-a"}},
				{Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-b"}},
			},
			replicaType: "",
			nodeToZone:  map[string]string{"node-a": "zone-a", "node-b": ""},
			expected:    map[string]int{"zone-a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeReplicasByZone(tt.replicas, tt.replicaType, tt.nodeToZone)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeAllowedZones(t *testing.T) {
	tests := []struct {
		name        string
		targetZones []string
		rscZones    []string
		nodeToZone  map[string]string
		expected    map[string]struct{}
	}{
		{
			name:        "uses targetZones when provided",
			targetZones: []string{"zone-a", "zone-b"},
			rscZones:    []string{"zone-c", "zone-d"},
			nodeToZone:  map[string]string{"node-1": "zone-e"},
			expected:    map[string]struct{}{"zone-a": {}, "zone-b": {}},
		},
		{
			name:        "uses rscZones when targetZones empty",
			targetZones: nil,
			rscZones:    []string{"zone-c", "zone-d"},
			nodeToZone:  map[string]string{"node-1": "zone-e"},
			expected:    map[string]struct{}{"zone-c": {}, "zone-d": {}},
		},
		{
			name:        "uses nodeToZone zones when both empty",
			targetZones: nil,
			rscZones:    nil,
			nodeToZone:  map[string]string{"node-1": "zone-e", "node-2": "zone-f", "node-3": ""},
			expected:    map[string]struct{}{"zone-e": {}, "zone-f": {}},
		},
		{
			name:        "empty result when all empty",
			targetZones: nil,
			rscZones:    nil,
			nodeToZone:  nil,
			expected:    map[string]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeAllowedZones(tt.targetZones, tt.rscZones, tt.nodeToZone)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeBestNode(t *testing.T) {
	tests := []struct {
		name       string
		candidates []NodeCandidate
		expected   NodeCandidate
	}{
		{
			name:       "empty input returns empty NodeCandidate",
			candidates: nil,
			expected:   NodeCandidate{},
		},
		{
			name: "single candidate returns that candidate",
			candidates: []NodeCandidate{
				{Name: "node-1", BestScore: 100, LVGCount: 1, SumScore: 100},
			},
			expected: NodeCandidate{Name: "node-1", BestScore: 100, LVGCount: 1, SumScore: 100},
		},
		{
			name: "sorts by BestScore first",
			candidates: []NodeCandidate{
				{Name: "node-1", BestScore: 50, LVGCount: 3, SumScore: 150},
				{Name: "node-2", BestScore: 100, LVGCount: 1, SumScore: 100},
			},
			expected: NodeCandidate{Name: "node-2", BestScore: 100, LVGCount: 1, SumScore: 100},
		},
		{
			name: "uses LVGCount as tiebreaker when BestScore equal",
			candidates: []NodeCandidate{
				{Name: "node-1", BestScore: 100, LVGCount: 1, SumScore: 100},
				{Name: "node-2", BestScore: 100, LVGCount: 3, SumScore: 150},
			},
			expected: NodeCandidate{Name: "node-2", BestScore: 100, LVGCount: 3, SumScore: 150},
		},
		{
			name: "uses SumScore as final tiebreaker",
			candidates: []NodeCandidate{
				{Name: "node-1", BestScore: 100, LVGCount: 2, SumScore: 150},
				{Name: "node-2", BestScore: 100, LVGCount: 2, SumScore: 200},
			},
			expected: NodeCandidate{Name: "node-2", BestScore: 100, LVGCount: 2, SumScore: 200},
		},
		{
			name: "complex multi-criteria sorting",
			candidates: []NodeCandidate{
				{Name: "node-1", BestScore: 80, LVGCount: 5, SumScore: 300},
				{Name: "node-2", BestScore: 100, LVGCount: 2, SumScore: 150},
				{Name: "node-3", BestScore: 100, LVGCount: 2, SumScore: 180},
				{Name: "node-4", BestScore: 100, LVGCount: 3, SumScore: 200},
			},
			expected: NodeCandidate{Name: "node-4", BestScore: 100, LVGCount: 3, SumScore: 200},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeBestNode(tt.candidates)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGroupCandidateNodesByZone(t *testing.T) {
	tests := []struct {
		name           string
		candidateNodes []string
		allowedZones   map[string]struct{}
		nodeToZone     map[string]string
		expected       map[string][]NodeCandidate
	}{
		{
			name:           "empty input",
			candidateNodes: nil,
			allowedZones:   map[string]struct{}{},
			nodeToZone:     map[string]string{},
			expected:       map[string][]NodeCandidate{},
		},
		{
			name:           "groups nodes by zone",
			candidateNodes: []string{"node-1", "node-2", "node-3"},
			allowedZones:   map[string]struct{}{"zone-a": {}, "zone-b": {}},
			nodeToZone:     map[string]string{"node-1": "zone-a", "node-2": "zone-a", "node-3": "zone-b"},
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1", Zone: "zone-a"}, {Name: "node-2", Zone: "zone-a"}},
				"zone-b": {{Name: "node-3", Zone: "zone-b"}},
			},
		},
		{
			name:           "excludes nodes without zone label",
			candidateNodes: []string{"node-1", "node-2"},
			allowedZones:   map[string]struct{}{"zone-a": {}},
			nodeToZone:     map[string]string{"node-1": "zone-a"},
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1", Zone: "zone-a"}},
			},
		},
		{
			name:           "excludes nodes with empty zone",
			candidateNodes: []string{"node-1", "node-2"},
			allowedZones:   map[string]struct{}{"zone-a": {}, "": {}},
			nodeToZone:     map[string]string{"node-1": "zone-a", "node-2": ""},
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1", Zone: "zone-a"}},
			},
		},
		{
			name:           "excludes nodes in disallowed zones",
			candidateNodes: []string{"node-1", "node-2", "node-3"},
			allowedZones:   map[string]struct{}{"zone-a": {}},
			nodeToZone:     map[string]string{"node-1": "zone-a", "node-2": "zone-b", "node-3": "zone-a"},
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1", Zone: "zone-a"}, {Name: "node-3", Zone: "zone-a"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := groupCandidateNodesByZone(tt.candidateNodes, tt.allowedZones, tt.nodeToZone)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSelectZoneWithMinReplicaCount(t *testing.T) {
	tests := []struct {
		name           string
		zoneCandidates map[string][]NodeCandidate
		replicaCounts  map[string]int
		expected       string
	}{
		{
			name:           "empty input",
			zoneCandidates: map[string][]NodeCandidate{},
			replicaCounts:  map[string]int{},
			expected:       "",
		},
		{
			name: "selects zone with minimum count",
			zoneCandidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
				"zone-b": {{Name: "node-2"}},
				"zone-c": {{Name: "node-3"}},
			},
			replicaCounts: map[string]int{"zone-a": 2, "zone-b": 1, "zone-c": 3},
			expected:      "zone-b",
		},
		{
			name: "ignores zones with no candidates",
			zoneCandidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
				"zone-b": {},
				"zone-c": {{Name: "node-3"}},
			},
			replicaCounts: map[string]int{"zone-a": 2, "zone-b": 0, "zone-c": 1},
			expected:      "zone-c",
		},
		{
			name: "treats missing count as zero",
			zoneCandidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
				"zone-b": {{Name: "node-2"}},
			},
			replicaCounts: map[string]int{"zone-a": 1},
			expected:      "zone-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := selectZoneWithMinReplicaCount(tt.zoneCandidates, tt.replicaCounts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeLVGToNodeFromEligible(t *testing.T) {
	tests := []struct {
		name     string
		eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
		expected map[string]LVGInfo
	}{
		{
			name:     "empty input",
			eligible: nil,
			expected: map[string]LVGInfo{},
		},
		{
			name: "extracts LVG info from nodes",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName: "node-1",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-1", Ready: true, ThinPoolName: "thin-1"},
					},
				},
				{
					NodeName: "node-2",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-2", Ready: true, ThinPoolName: ""},
					},
				},
			},
			expected: map[string]LVGInfo{
				"vg-1": {NodeName: "node-1", ThinPoolName: "thin-1"},
				"vg-2": {NodeName: "node-2", ThinPoolName: ""},
			},
		},
		{
			name: "skips LVGs with Ready=false",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName: "node-1",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-ready", Ready: true},
						{Name: "vg-not-ready", Ready: false},
					},
				},
			},
			expected: map[string]LVGInfo{
				"vg-ready": {NodeName: "node-1", ThinPoolName: ""},
			},
		},
		{
			name: "skips LVGs with Unschedulable=true",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName: "node-1",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-schedulable", Ready: true, Unschedulable: false},
						{Name: "vg-unschedulable", Ready: true, Unschedulable: true},
					},
				},
			},
			expected: map[string]LVGInfo{
				"vg-schedulable": {NodeName: "node-1", ThinPoolName: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeLVGToNodeFromEligible(tt.eligible)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeStoragePoolType(t *testing.T) {
	tests := []struct {
		name     string
		eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
		expected string
	}{
		{
			name:     "empty input returns LVM",
			eligible: nil,
			expected: "LVM",
		},
		{
			name: "returns LVMThin when ThinPoolName is set",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName: "node-1",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-1", ThinPoolName: "thin-pool"},
					},
				},
			},
			expected: "LVMThin",
		},
		{
			name: "returns LVM when no ThinPoolName",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{
					NodeName: "node-1",
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
						{Name: "vg-1", ThinPoolName: ""},
					},
				},
			},
			expected: "LVM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeStoragePoolType(tt.eligible)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeNodeToZoneFromEligible(t *testing.T) {
	tests := []struct {
		name     string
		eligible []v1alpha1.ReplicatedStoragePoolEligibleNode
		expected map[string]string
	}{
		{
			name:     "empty input",
			eligible: nil,
			expected: map[string]string{},
		},
		{
			name: "extracts node to zone mapping",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", ZoneName: "zone-a"},
				{NodeName: "node-2", ZoneName: "zone-b"},
				{NodeName: "node-3", ZoneName: "zone-a"},
			},
			expected: map[string]string{
				"node-1": "zone-a",
				"node-2": "zone-b",
				"node-3": "zone-a",
			},
		},
		{
			name: "handles empty zone names",
			eligible: []v1alpha1.ReplicatedStoragePoolEligibleNode{
				{NodeName: "node-1", ZoneName: "zone-a"},
				{NodeName: "node-2", ZoneName: ""},
			},
			expected: map[string]string{
				"node-1": "zone-a",
				"node-2": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeNodeToZoneFromEligible(tt.eligible)
			assert.Equal(t, tt.expected, result)
		})
	}
}
