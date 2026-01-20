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

package runners

import (
	"context"
	"log/slog"
	"math/rand"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/chaos"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
)

// ChaosNetworkPartitioner periodically creates network partitions (split-brain scenarios)
type ChaosNetworkPartitioner struct {
	cfg              config.ChaosNetworkPartitionerConfig
	ciliumManager    *chaos.CiliumPolicyManager
	parentClient     *chaos.ParentClient
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewChaosNetworkPartitioner creates a new ChaosNetworkPartitioner
func NewChaosNetworkPartitioner(
	cfg config.ChaosNetworkPartitionerConfig,
	ciliumManager *chaos.CiliumPolicyManager,
	parentClient *chaos.ParentClient,
	forceCleanupChan <-chan struct{},
) *ChaosNetworkPartitioner {
	return &ChaosNetworkPartitioner{
		cfg:              cfg,
		ciliumManager:    ciliumManager,
		parentClient:     parentClient,
		forceCleanupChan: forceCleanupChan,
		log:              slog.Default().With("runner", "chaos-network-partitioner"),
	}
}

// Run starts the network partition cycle until context is cancelled
func (c *ChaosNetworkPartitioner) Run(ctx context.Context) error {
	c.log.Info("started")
	defer c.log.Info("finished")

	for {
		// Wait random duration before next incident
		if err := waitRandomWithContext(ctx, c.cfg.Period); err != nil {
			return err
		}

		// Perform partitioning
		if err := c.doPartition(ctx); err != nil {
			c.log.Error("partitioning failed", "error", err)
		}
	}
}

func (c *ChaosNetworkPartitioner) doPartition(ctx context.Context) error {
	// Get list of nodes from parent cluster
	nodes, err := c.parentClient.ListVMs(ctx)
	if err != nil {
		return err
	}

	if len(nodes) < 2 {
		c.log.Debug("not enough nodes for network partition", "node_count", len(nodes))
		return nil
	}

	// Split nodes into two groups
	groupA, groupB := c.splitNodes(nodes)

	c.log.Info("creating network partition",
		"group_a_size", len(groupA),
		"group_b_size", len(groupB),
		"group_a_nodes", nodeNames(groupA),
		"group_b_nodes", nodeNames(groupB),
	)

	// Create blocking policies between groups (bidirectional)
	var policyNames []string
	for _, nodeA := range groupA {
		for _, nodeB := range groupB {
			// Block A -> B
			policyName, err := c.ciliumManager.BlockAllNetwork(ctx, nodeA, nodeB)
			if err != nil {
				c.log.Error("failed to block network", "node_a", nodeA.Name, "node_b", nodeB.Name, "error", err)
				continue
			}
			policyNames = append(policyNames, policyName)

			// Block B -> A
			policyName, err = c.ciliumManager.BlockAllNetwork(ctx, nodeB, nodeA)
			if err != nil {
				c.log.Error("failed to block network", "node_a", nodeB.Name, "node_b", nodeA.Name, "error", err)
				continue
			}
			policyNames = append(policyNames, policyName)
		}
	}

	c.log.Info("network partition created", "policy_count", len(policyNames))

	// Wait for incident duration or context cancellation
	incidentDuration := randomDuration(c.cfg.IncidentDuration)
	c.log.Debug("keeping network partitioned", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup(policyNames)
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup(policyNames)
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, remove partition
	}

	// Remove partition
	c.log.Info("removing network partition", "policy_count", len(policyNames))
	c.cleanup(policyNames)

	return nil
}

func (c *ChaosNetworkPartitioner) cleanup(policyNames []string) {
	for _, policyName := range policyNames {
		if err := c.ciliumManager.UnblockTraffic(context.Background(), policyName); err != nil {
			c.log.Error("cleanup failed", "policy", policyName, "error", err)
		}
	}
}

func (c *ChaosNetworkPartitioner) splitNodes(nodes []chaos.NodeInfo) ([]chaos.NodeInfo, []chaos.NodeInfo) {
	// Shuffle nodes first
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	// Split into two groups
	// If GroupSize is specified, use it as target size for group A
	// Otherwise, split roughly in half
	splitPoint := len(nodes) / 2
	if c.cfg.GroupSize > 0 && c.cfg.GroupSize < len(nodes) {
		splitPoint = c.cfg.GroupSize
	}

	// Ensure at least 1 node in each group
	if splitPoint < 1 {
		splitPoint = 1
	}
	if splitPoint >= len(nodes) {
		splitPoint = len(nodes) - 1
	}

	return nodes[:splitPoint], nodes[splitPoint:]
}

// nodeNames extracts names from NodeInfo slice for logging
func nodeNames(nodes []chaos.NodeInfo) []string {
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Name
	}
	return names
}
