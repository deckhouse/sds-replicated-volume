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

package chaos

import (
	"context"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// DRBDPortCollector collects actual DRBD ports from ReplicatedVolumeReplica CRDs.
// Uses RVR.status.drbd.config.address.port to get ports for each node.
type DRBDPortCollector struct {
	cl client.Client
}

// NewDRBDPortCollector creates a new DRBDPortCollector
func NewDRBDPortCollector(cl client.Client) *DRBDPortCollector {
	return &DRBDPortCollector{cl: cl}
}

// CollectPortsFromAllNodes collects DRBD ports for the specified nodes from RVR CRDs.
// Returns deduplicated sorted list of ports used by RVRs on these nodes.
func (c *DRBDPortCollector) CollectPortsFromAllNodes(ctx context.Context, nodes []NodeInfo) ([]int, error) {
	// Build a set of node names for fast lookup
	nodeNames := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		nodeNames[node.Name] = true
	}

	// List all RVRs
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := c.cl.List(ctx, rvrList); err != nil {
		return nil, err
	}

	// Collect ports from RVRs on the specified nodes
	portsMap := make(map[int]bool)
	for _, rvr := range rvrList.Items {
		// Skip RVRs not on our target nodes
		if !nodeNames[rvr.Spec.NodeName] {
			continue
		}

		// Get port from RVR status
		port := c.getPortFromRVR(&rvr)
		if port > 0 {
			portsMap[port] = true
		}
	}

	// Convert map to sorted slice
	result := make([]int, 0, len(portsMap))
	for port := range portsMap {
		result = append(result, port)
	}
	sort.Ints(result)

	return result, nil
}

// getPortFromRVR extracts DRBD port from RVR status.
// Returns 0 if port is not configured.
func (c *DRBDPortCollector) getPortFromRVR(rvr *v1alpha1.ReplicatedVolumeReplica) int {
	if rvr.Status.DRBD == nil {
		return 0
	}
	if rvr.Status.DRBD.Config == nil {
		return 0
	}
	if rvr.Status.DRBD.Config.Address == nil {
		return 0
	}
	return int(rvr.Status.DRBD.Config.Address.Port)
}

// CleanupCollectorJobs is a no-op since we no longer use Jobs for port collection.
// Kept for interface compatibility.
func (c *DRBDPortCollector) CleanupCollectorJobs(_ context.Context) error {
	return nil
}
