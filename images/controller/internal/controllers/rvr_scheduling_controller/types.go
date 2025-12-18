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
	log                            logr.Logger
	Rv                             *v1alpha3.ReplicatedVolume
	Rsc                            *v1alpha1.ReplicatedStorageClass
	Rsp                            *v1alpha1.ReplicatedStoragePool
	RvrList                        []*v1alpha3.ReplicatedVolumeReplica
	RvPublishOnNodes               []string
	NodesOfRvReplicas              []string
	PublishOnNodesWithoutRvReplica []string
	UnscheduledDiskfulReplicas     []*v1alpha3.ReplicatedVolumeReplica
	ScheduledDiskfulReplicas       []*v1alpha3.ReplicatedVolumeReplica
	SpLvgToNodeInfoMap             map[string]LvgNodeInfo // {lvgName: {NodeName, ThinPoolName}}
	RscNodes                       []string
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
