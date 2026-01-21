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

package world

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// WorldGate provides access to WorldView for a given RSC configuration.
type WorldGate interface {
	GetWorldView(ctx context.Context, key WorldKey) (WorldView, error)
}

// WorldBus provides event sources for world and node changes.
type WorldBus interface {
	WorldSource() <-chan event.TypedGenericEvent[WorldKey]
	NodeSource() <-chan event.TypedGenericEvent[NodeName]
}

// WorldKey uniquely identifies a WorldView by RSC name and configuration generation.
type WorldKey struct {
	RSCName                 string
	ConfigurationGeneration int64
}

// NodeName is a Kubernetes node name.
type NodeName string

// NodeInfo contains information about a node.
type NodeInfo struct {
	Ready      bool
	AgentReady bool
}

// Nodes is a map of node names to their info.
type Nodes map[NodeName]NodeInfo

// WorldView represents a snapshot of the world state for eligible nodes computation.
type WorldView struct {
	EligibleNodes                 EligibleNodes
	EligibleNodesComputationError *EligibleNodesComputationError
	Nodes                         Nodes
}

// EligibleNodeInfo represents an eligible node with its LVM volume groups.
type EligibleNodeInfo struct {
	NodeName        string
	Zone            string
	LVMVolumeGroups []EligibleNodeLVMVolumeGroup
	Unschedulable   bool
	Ready           bool
	AgentReady      bool
}

// EligibleNodeLVMVolumeGroup represents an LVM volume group on an eligible node.
type EligibleNodeLVMVolumeGroup struct {
	Name          string
	ThinPoolName  string
	Unschedulable bool
}

// EligibleNodes is a map of node names to eligible node info.
type EligibleNodes map[NodeName]EligibleNodeInfo

// EligibleNodesComputationReason represents the reason for an eligible nodes computation error.
type EligibleNodesComputationReason string

const (
	// EligibleNodesComputationReasonRSPNotFound indicates that the referenced RSP does not exist.
	EligibleNodesComputationReasonRSPNotFound EligibleNodesComputationReason = v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonReplicatedStoragePoolNotFound

	// EligibleNodesComputationReasonLVGNotFound indicates that one or more referenced LVGs do not exist.
	EligibleNodesComputationReasonLVGNotFound EligibleNodesComputationReason = v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonLVMVolumeGroupNotFound

	// EligibleNodesComputationReasonInvalidStoragePoolOrLVG indicates that RSP or LVG is invalid or not ready
	// (e.g., RSP phase is not Completed, ThinPool not found in LVG).
	EligibleNodesComputationReasonInvalidStoragePoolOrLVG EligibleNodesComputationReason = v1alpha1.ReplicatedStorageClassCondEligibleNodesCalculatedReasonInvalidStoragePoolOrLVG
)

// EligibleNodesComputationError represents an error during eligible nodes computation.
type EligibleNodesComputationError struct {
	Reason  EligibleNodesComputationReason
	Message string
}

func (e *EligibleNodesComputationError) Error() string {
	return e.Message
}

// ValidateEligibleNodes checks if there are enough eligible nodes for the given replication and topology.
// Returns nil if validation passes, or an error describing the problem.
func (wv WorldView) ValidateEligibleNodes(
	replication v1alpha1.ReplicatedStorageClassReplication,
	topology v1alpha1.ReplicatedStorageClassTopology,
	zones []string,
) error {
	// No validation needed for ReplicationNone
	if replication == v1alpha1.ReplicationNone {
		return nil
	}

	// Count schedulable nodes per zone
	zoneNodesCount := make(map[string]int)
	totalSchedulable := 0
	for _, node := range wv.EligibleNodes {
		if node.Unschedulable {
			continue
		}
		totalSchedulable++
		if node.Zone != "" {
			zoneNodesCount[node.Zone]++
		}
	}

	switch topology {
	case v1alpha1.RSCTopologyTransZonal:
		// Each specified zone must have at least 1 schedulable node
		for _, zone := range zones {
			if zoneNodesCount[zone] < 1 {
				return fmt.Errorf("zone %q has no schedulable nodes", zone)
			}
		}

	case v1alpha1.RSCTopologyZonal:
		// At least one zone must have 3+ schedulable nodes for quorum
		var hasQuorumZone bool
		for _, count := range zoneNodesCount {
			if count >= 3 {
				hasQuorumZone = true
				break
			}
		}
		if !hasQuorumZone {
			return fmt.Errorf("no zone has enough schedulable nodes for quorum (need at least 3)")
		}

	case v1alpha1.RSCTopologyIgnored:
		// Need 3+ schedulable nodes total for quorum
		if totalSchedulable < 3 {
			return fmt.Errorf("not enough schedulable nodes for quorum: have %d, need at least 3", totalSchedulable)
		}
	}

	return nil
}
