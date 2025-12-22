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

package rvr_scheduling_controller

import (
	"slices"

	"github.com/go-logr/logr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type SchedulingContext struct {
	Log                            logr.Logger
	Rv                             *v1alpha3.ReplicatedVolume
	Rsc                            *v1alpha1.ReplicatedStorageClass
	Rsp                            *v1alpha1.ReplicatedStoragePool
	RvrList                        []*v1alpha3.ReplicatedVolumeReplica
	PublishOnNodes                 []string
	NodesWithAnyReplica            map[string]struct{}
	PublishOnNodesWithoutRvReplica []string
	UnscheduledDiskfulReplicas     []*v1alpha3.ReplicatedVolumeReplica
	ScheduledDiskfulReplicas       []*v1alpha3.ReplicatedVolumeReplica
	UnscheduledAccessReplicas      []*v1alpha3.ReplicatedVolumeReplica
	UnscheduledTieBreakerReplicas  []*v1alpha3.ReplicatedVolumeReplica
	RspLvgToNodeInfoMap            map[string]LvgInfo // {lvgName: {NodeName, ThinPoolName}}
	RspNodesWithoutReplica         []string
	NodeNameToZone                 map[string]string          // {nodeName: zoneName}
	ZonesToNodeCandidatesMap       map[string][]NodeCandidate // {zone1: [{name: node1, score: 100}, {name: node2, score: 90}]}
	// RVRs with nodes assigned in this reconcile
	RVRsToSchedule []*v1alpha3.ReplicatedVolumeReplica
}

type NodeCandidate struct {
	Name  string
	Score int
}

// SelectAndRemoveBestNode sorts candidates by score (descending), selects the best one,
// removes it from the slice, and returns the node name along with the updated slice.
// Returns empty string and original slice if no candidates available.
func SelectAndRemoveBestNode(candidates []NodeCandidate) (string, []NodeCandidate) {
	if len(candidates) == 0 {
		return "", candidates
	}

	// Sort by score descending (higher score = better)
	slices.SortFunc(candidates, func(a, b NodeCandidate) int {
		return b.Score - a.Score
	})

	// Select the best node and remove it from the slice
	bestNode := candidates[0].Name
	return bestNode, candidates[1:]
}

type LvgInfo struct {
	NodeName     string
	ThinPoolName string
}

// UpdateAfterScheduling updates the scheduling context after replicas have been assigned nodes.
// It removes assigned replicas from the appropriate unscheduled list based on their type,
// adds them to ScheduledDiskfulReplicas (for Diskful type),
// adds the assigned nodes to NodesWithAnyReplica, and removes them from PublishOnNodesWithoutRvReplica.
func (sctx *SchedulingContext) UpdateAfterScheduling(assignedReplicas []*v1alpha3.ReplicatedVolumeReplica) {
	if len(assignedReplicas) == 0 {
		return
	}

	// Build a set of assigned replica names for fast lookup
	assignedSet := make(map[string]struct{}, len(assignedReplicas))
	for _, rvr := range assignedReplicas {
		assignedSet[rvr.Name] = struct{}{}
	}

	// Determine replica type from first replica (all in batch should be same type)
	replicaType := assignedReplicas[0].Spec.Type

	// Remove assigned replicas from appropriate unscheduled list based on type
	switch replicaType {
	case v1alpha3.ReplicaTypeDiskful:
		var remainingUnscheduled []*v1alpha3.ReplicatedVolumeReplica
		for _, rvr := range sctx.UnscheduledDiskfulReplicas {
			if _, assigned := assignedSet[rvr.Name]; !assigned {
				remainingUnscheduled = append(remainingUnscheduled, rvr)
			}
		}
		sctx.UnscheduledDiskfulReplicas = remainingUnscheduled
		// Add assigned Diskful replicas to ScheduledDiskfulReplicas
		sctx.ScheduledDiskfulReplicas = append(sctx.ScheduledDiskfulReplicas, assignedReplicas...)

	case v1alpha3.ReplicaTypeAccess:
		var remainingUnscheduled []*v1alpha3.ReplicatedVolumeReplica
		for _, rvr := range sctx.UnscheduledAccessReplicas {
			if _, assigned := assignedSet[rvr.Name]; !assigned {
				remainingUnscheduled = append(remainingUnscheduled, rvr)
			}
		}
		sctx.UnscheduledAccessReplicas = remainingUnscheduled

	case v1alpha3.ReplicaTypeTieBreaker:
		var remainingUnscheduled []*v1alpha3.ReplicatedVolumeReplica
		for _, rvr := range sctx.UnscheduledTieBreakerReplicas {
			if _, assigned := assignedSet[rvr.Name]; !assigned {
				remainingUnscheduled = append(remainingUnscheduled, rvr)
			}
		}
		sctx.UnscheduledTieBreakerReplicas = remainingUnscheduled
	}

	// Build a set of assigned nodes and add to NodesWithAnyReplica
	assignedNodes := make(map[string]struct{}, len(assignedReplicas))
	for _, rvr := range assignedReplicas {
		nodeName := rvr.Spec.NodeName
		assignedNodes[nodeName] = struct{}{}
		sctx.NodesWithAnyReplica[nodeName] = struct{}{}
	}

	// Remove assigned nodes from PublishOnNodesWithoutRvReplica
	var remainingPublishNodes []string
	for _, node := range sctx.PublishOnNodesWithoutRvReplica {
		if _, assigned := assignedNodes[node]; !assigned {
			remainingPublishNodes = append(remainingPublishNodes, node)
		}
	}
	sctx.PublishOnNodesWithoutRvReplica = remainingPublishNodes

	// Add assigned replicas to RVRsToSchedule
	sctx.RVRsToSchedule = append(sctx.RVRsToSchedule, assignedReplicas...)
}

const publishOnScoreBonus = 1000

// ApplyPublishOnBonus increases score for nodes in rv.spec.publishOn.
// This ensures publishOn nodes are preferred when scheduling Diskful replicas.
func (sctx *SchedulingContext) ApplyPublishOnBonus() {
	if len(sctx.PublishOnNodes) == 0 {
		return
	}

	publishOnSet := make(map[string]struct{}, len(sctx.PublishOnNodes))
	for _, node := range sctx.PublishOnNodes {
		publishOnSet[node] = struct{}{}
	}

	for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
		for i := range candidates {
			if _, isPublishOn := publishOnSet[candidates[i].Name]; isPublishOn {
				candidates[i].Score += publishOnScoreBonus
			}
		}
		sctx.ZonesToNodeCandidatesMap[zone] = candidates
	}
}

// findZoneWithMinReplicaCount finds the zone with the minimum replica count among the given zones.
// Returns the zone name and its replica count. If zones is empty, returns ("", -1).
func findZoneWithMinReplicaCount(zones map[string]struct{}, zoneReplicaCount map[string]int) (string, int) {
	var minZone string
	minCount := -1
	for zone := range zones {
		count := zoneReplicaCount[zone]
		if minCount == -1 || count < minCount {
			minCount = count
			minZone = zone
		}
	}
	return minZone, minCount
}
