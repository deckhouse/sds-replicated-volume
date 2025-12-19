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
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
)

type SchedulingContext struct {
	Log                            logr.Logger
	Rv                             *v1alpha3.ReplicatedVolume
	Rsc                            *v1alpha1.ReplicatedStorageClass
	Rsp                            *v1alpha1.ReplicatedStoragePool
	RvrList                        []*v1alpha3.ReplicatedVolumeReplica
	RvPublishOnNodes               []string
	NodesWithAnyReplica            map[string]struct{}
	PublishOnNodesWithoutRvReplica []string
	UnscheduledDiskfulReplicas     []*v1alpha3.ReplicatedVolumeReplica
	ScheduledDiskfulReplicas       []*v1alpha3.ReplicatedVolumeReplica
	RspLvgToNodeInfoMap            map[string]LvgNodeInfo // {lvgName: {NodeName, ThinPoolName}}
	RspNodes                       []string
	NodeNameToZone                 map[string]string          // {nodeName: zoneName}
	ZonesToNodeCandidatesMap       map[string][]NodeCandidate // {zone1: [{name: node1, score: 100}, {name: node2, score: 90}]}
	// RVRs with nodes assigned in this reconcile
	RVRsToSchedule []*v1alpha3.ReplicatedVolumeReplica
}

type NodeCandidate struct {
	Name  string
	Score int
}

type LvgNodeInfo struct {
	NodeName     string
	ThinPoolName string
}

// UpdateAfterScheduling updates the scheduling context after replicas have been assigned nodes.
// It moves assigned replicas from UnscheduledDiskfulReplicas to ScheduledDiskfulReplicas,
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

	// Remove assigned replicas from UnscheduledDiskfulReplicas
	var remainingUnscheduled []*v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range sctx.UnscheduledDiskfulReplicas {
		if _, assigned := assignedSet[rvr.Name]; !assigned {
			remainingUnscheduled = append(remainingUnscheduled, rvr)
		}
	}
	sctx.UnscheduledDiskfulReplicas = remainingUnscheduled

	// Add assigned replicas to ScheduledDiskfulReplicas
	sctx.ScheduledDiskfulReplicas = append(sctx.ScheduledDiskfulReplicas, assignedReplicas...)

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
	if len(sctx.RvPublishOnNodes) == 0 {
		return
	}

	publishOnSet := make(map[string]struct{}, len(sctx.RvPublishOnNodes))
	for _, node := range sctx.RvPublishOnNodes {
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
