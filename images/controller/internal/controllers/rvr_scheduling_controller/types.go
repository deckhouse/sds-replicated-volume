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
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type SchedulingContext struct {
	RV              *v1alpha1.ReplicatedVolume
	RSC             *v1alpha1.ReplicatedStorageClass
	RSP             *v1alpha1.ReplicatedStoragePool
	EligibleNodes   []v1alpha1.ReplicatedStoragePoolEligibleNode
	AttachToNodes   []string
	NodeToZone      map[string]string
	LVGToNode       map[string]LVGInfo
	StoragePoolType string

	AllRVRs               []*v1alpha1.ReplicatedVolumeReplica
	ScheduledDiskful      []*v1alpha1.ReplicatedVolumeReplica
	UnscheduledDiskful    []*v1alpha1.ReplicatedVolumeReplica
	ScheduledTieBreaker   []*v1alpha1.ReplicatedVolumeReplica
	UnscheduledTieBreaker []*v1alpha1.ReplicatedVolumeReplica
	OccupiedNodes         map[string]struct{}

	// ZoneCandidates holds scored candidates per zone (computed once for Diskful phase).
	ZoneCandidates map[string][]NodeCandidate

	// SelectedZone is the zone selected for Zonal topology (determined by first Diskful).
	SelectedZone string

	// ZoneReplicaCounts tracks replica counts per zone for TransZonal topology.
	// Updated after each successful scheduling.
	ZoneReplicaCounts map[string]int
}

// ScheduledRVRs returns all scheduled RVRs (both Diskful and TieBreaker).
func (sctx *SchedulingContext) ScheduledRVRs() []*v1alpha1.ReplicatedVolumeReplica {
	result := make([]*v1alpha1.ReplicatedVolumeReplica, 0, len(sctx.ScheduledDiskful)+len(sctx.ScheduledTieBreaker))
	result = append(result, sctx.ScheduledDiskful...)
	result = append(result, sctx.ScheduledTieBreaker...)
	return result
}

// MarkNodeOccupied marks a node as occupied so it won't be used for other replicas.
func (sctx *SchedulingContext) MarkNodeOccupied(nodeName string) {
	if sctx.OccupiedNodes == nil {
		sctx.OccupiedNodes = make(map[string]struct{})
	}
	sctx.OccupiedNodes[nodeName] = struct{}{}
}

// RemoveCandidate removes a node from ZoneCandidates after successful scheduling.
// This ensures the next RVR won't try to use the same node.
func (sctx *SchedulingContext) RemoveCandidate(nodeName string) {
	for zone, candidates := range sctx.ZoneCandidates {
		filtered := make([]NodeCandidate, 0, len(candidates))
		for _, c := range candidates {
			if c.Name != nodeName {
				filtered = append(filtered, c)
			}
		}
		sctx.ZoneCandidates[zone] = filtered
	}
}

// IncrementZoneReplicaCount increments the replica count for a zone (for TransZonal topology).
func (sctx *SchedulingContext) IncrementZoneReplicaCount(zone string) {
	if sctx.ZoneReplicaCounts == nil {
		sctx.ZoneReplicaCounts = make(map[string]int)
	}
	sctx.ZoneReplicaCounts[zone]++
}

type NodeCandidate struct {
	Name         string
	Zone         string
	BestScore    int    // highest LVG score (primary sort key)
	LVGCount     int    // number of suitable LVGs (secondary sort key)
	SumScore     int    // sum of all LVG scores (tertiary sort key)
	LVGName      string // best LVG name for this node
	ThinPoolName string // thin pool name (if LVMThin)
}

type LVGInfo struct {
	NodeName     string
	ThinPoolName string
}
