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

// ChaosNetworkBlocker periodically blocks all network between random node pairs
type ChaosNetworkBlocker struct {
	cfg              config.ChaosNetworkBlockerConfig
	ciliumManager    *chaos.CiliumPolicyManager
	parentClient     *chaos.ParentClient
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewChaosNetworkBlocker creates a new ChaosNetworkBlocker
func NewChaosNetworkBlocker(
	cfg config.ChaosNetworkBlockerConfig,
	ciliumManager *chaos.CiliumPolicyManager,
	parentClient *chaos.ParentClient,
	forceCleanupChan <-chan struct{},
) *ChaosNetworkBlocker {
	return &ChaosNetworkBlocker{
		cfg:              cfg,
		ciliumManager:    ciliumManager,
		parentClient:     parentClient,
		forceCleanupChan: forceCleanupChan,
		log:              slog.Default().With("runner", "chaos-network-blocker"),
	}
}

// Run starts the network blocking cycle until context is cancelled
func (c *ChaosNetworkBlocker) Run(ctx context.Context) error {
	c.log.Info("started")
	defer c.log.Info("finished")

	for {
		// Wait random duration before next incident
		if err := waitRandomWithContext(ctx, c.cfg.Period); err != nil {
			return err
		}

		// Perform blocking
		if err := c.doBlock(ctx); err != nil {
			c.log.Error("blocking failed", "error", err)
		}
	}
}

func (c *ChaosNetworkBlocker) doBlock(ctx context.Context) error {
	// Get list of nodes from parent cluster
	nodes, err := c.parentClient.ListVMs(ctx)
	if err != nil {
		return err
	}

	if len(nodes) < 2 {
		c.log.Debug("not enough nodes for network blocking", "node_count", len(nodes))
		return nil
	}

	// Select random pair of nodes
	nodeA, nodeB := c.randomNodePair(nodes)

	c.log.Info("blocking all network",
		"node_a", nodeA.Name,
		"node_a_ip", nodeA.IPAddress,
		"node_b", nodeB.Name,
		"node_b_ip", nodeB.IPAddress,
	)

	// Create blocking policy (one-directional: nodeA blocks traffic to/from nodeB).
	// Note: This is intentional - even one-sided network issues will break DRBD replication
	// and trigger failover scenarios. For bidirectional blocking use chaos-network-partitioner.
	policyName, err := c.ciliumManager.BlockAllNetwork(ctx, nodeA, nodeB)
	if err != nil {
		return err
	}

	// Wait for incident duration or context cancellation
	incidentDuration := randomDuration(c.cfg.IncidentDuration)
	c.log.Debug("keeping network blocked", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup(policyName)
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup(policyName)
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, unblock
	}

	// Unblock traffic
	c.log.Info("unblocking network",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
		"policy", policyName,
	)

	if err := c.ciliumManager.UnblockTraffic(context.Background(), policyName); err != nil {
		c.log.Error("failed to unblock network", "error", err)
	}

	return nil
}

func (c *ChaosNetworkBlocker) cleanup(policyName string) {
	c.log.Info("cleanup: unblocking network", "policy", policyName)
	if err := c.ciliumManager.UnblockTraffic(context.Background(), policyName); err != nil {
		c.log.Error("cleanup failed", "error", err)
	}
}

func (c *ChaosNetworkBlocker) randomNodePair(nodes []chaos.NodeInfo) (chaos.NodeInfo, chaos.NodeInfo) {
	// Shuffle nodes
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	return nodes[0], nodes[1]
}
