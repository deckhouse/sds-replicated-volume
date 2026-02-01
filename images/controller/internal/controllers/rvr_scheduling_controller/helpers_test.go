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
	"fmt"
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

func TestIsSchedulingError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error returns false",
			err:      nil,
			expected: false,
		},
		{
			name:     "errSchedulingPending returns true",
			err:      errSchedulingPending,
			expected: true,
		},
		{
			name:     "errSchedulingTopologyConflict returns true",
			err:      errSchedulingTopologyConflict,
			expected: true,
		},
		{
			name:     "errSchedulingNoCandidateNodes returns true",
			err:      errSchedulingNoCandidateNodes,
			expected: true,
		},
		{
			name:     "wrapped errSchedulingPending returns true",
			err:      fmt.Errorf("context: %w", errSchedulingPending),
			expected: true,
		},
		{
			name:     "wrapped errSchedulingTopologyConflict returns true",
			err:      fmt.Errorf("context: %w", errSchedulingTopologyConflict),
			expected: true,
		},
		{
			name:     "wrapped errSchedulingNoCandidateNodes returns true",
			err:      fmt.Errorf("context: %w", errSchedulingNoCandidateNodes),
			expected: true,
		},
		{
			name:     "generic error returns false",
			err:      fmt.Errorf("some API error"),
			expected: false,
		},
		{
			name:     "not found error returns false",
			err:      fmt.Errorf("resource not found"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSchedulingError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSelectBestCandidateForTieBreaker(t *testing.T) {
	tests := []struct {
		name             string
		sctx             *SchedulingContext
		expectedNode     string
		expectedZone     string
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name: "Ignored topology - returns first available",
			sctx: &SchedulingContext{
				Topology: "Ignored",
				TieBreakerCandidates: map[string][]NodeCandidate{
					"Ignored": {
						{Name: "node-1"},
						{Name: "node-2"},
					},
				},
			},
			expectedNode: "node-1",
		},
		{
			name: "Zonal topology - returns first in selected zone",
			sctx: &SchedulingContext{
				Topology:     "Zonal",
				SelectedZone: "zone-a",
				TieBreakerCandidates: map[string][]NodeCandidate{
					"zone-a": {{Name: "node-a1"}, {Name: "node-a2"}},
					"zone-b": {{Name: "node-b1"}},
				},
			},
			expectedNode: "node-a1",
		},
		{
			name: "TransZonal topology - selects zone with min replicas",
			sctx: &SchedulingContext{
				Topology: "TransZonal",
				ZoneReplicaCounts: map[string]int{
					"zone-a": 2,
					"zone-b": 1,
				},
				TieBreakerCandidates: map[string][]NodeCandidate{
					"zone-a": {{Name: "node-a1", Zone: "zone-a"}},
					"zone-b": {{Name: "node-b1", Zone: "zone-b"}},
				},
			},
			expectedNode: "node-b1",
			expectedZone: "zone-b",
		},
		{
			name: "empty TieBreakerCandidates - returns error",
			sctx: &SchedulingContext{
				Topology:             "Ignored",
				TieBreakerCandidates: map[string][]NodeCandidate{},
			},
			expectedError:    true,
			expectedErrorMsg: "no TieBreaker candidates available",
		},
		{
			name: "Zonal with no zone selected - returns error",
			sctx: &SchedulingContext{
				Topology:     "Zonal",
				SelectedZone: "",
				TieBreakerCandidates: map[string][]NodeCandidate{
					"zone-a": {{Name: "node-a1"}},
				},
			},
			expectedError:    true,
			expectedErrorMsg: "no zone selected",
		},
		{
			name: "Zonal with empty candidates in selected zone - returns error",
			sctx: &SchedulingContext{
				Topology:     "Zonal",
				SelectedZone: "zone-a",
				TieBreakerCandidates: map[string][]NodeCandidate{
					"zone-a": {},
					"zone-b": {{Name: "node-b1"}},
				},
			},
			expectedError:    true,
			expectedErrorMsg: "no candidates in zone zone-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := selectBestCandidateForTieBreaker(tt.sctx)
			if tt.expectedError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedNode, result.Name)
				if tt.expectedZone != "" {
					assert.Equal(t, tt.expectedZone, result.Zone)
				}
			}
		})
	}
}

func TestRemoveTieBreakerCandidate(t *testing.T) {
	tests := []struct {
		name       string
		candidates map[string][]NodeCandidate
		removeNode string
		expected   map[string][]NodeCandidate
	}{
		{
			name: "removes node from single zone",
			candidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}, {Name: "node-2"}},
			},
			removeNode: "node-1",
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-2"}},
			},
		},
		{
			name: "removes node from multiple zones",
			candidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}, {Name: "node-2"}},
				"zone-b": {{Name: "node-3"}},
			},
			removeNode: "node-2",
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
				"zone-b": {{Name: "node-3"}},
			},
		},
		{
			name: "no-op when node not found",
			candidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
			},
			removeNode: "node-nonexistent",
			expected: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
			},
		},
		{
			name: "removes last node in zone (leaves empty slice)",
			candidates: map[string][]NodeCandidate{
				"zone-a": {{Name: "node-1"}},
				"zone-b": {{Name: "node-2"}},
			},
			removeNode: "node-1",
			expected: map[string][]NodeCandidate{
				"zone-a": {},
				"zone-b": {{Name: "node-2"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sctx := &SchedulingContext{
				TieBreakerCandidates: tt.candidates,
			}
			sctx.RemoveTieBreakerCandidate(tt.removeNode)
			assert.Equal(t, tt.expected, sctx.TieBreakerCandidates)
		})
	}
}
