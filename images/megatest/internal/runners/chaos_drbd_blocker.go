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

// ChaosDRBDBlocker periodically blocks DRBD ports between random node pairs
type ChaosDRBDBlocker struct {
	cfg              config.ChaosDRBDBlockerConfig
	ciliumManager    *chaos.CiliumPolicyManager
	parentClient     *chaos.ParentClient
	portCollector    *chaos.DRBDPortCollector
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewChaosDRBDBlocker creates a new ChaosDRBDBlocker
func NewChaosDRBDBlocker(
	cfg config.ChaosDRBDBlockerConfig,
	ciliumManager *chaos.CiliumPolicyManager,
	parentClient *chaos.ParentClient,
	portCollector *chaos.DRBDPortCollector,
	forceCleanupChan <-chan struct{},
) *ChaosDRBDBlocker {
	return &ChaosDRBDBlocker{
		cfg:              cfg,
		ciliumManager:    ciliumManager,
		parentClient:     parentClient,
		portCollector:    portCollector,
		forceCleanupChan: forceCleanupChan,
		log:              slog.Default().With("runner", "chaos-drbd-blocker"),
	}
}

// Run starts the DRBD blocking cycle until context is cancelled
func (c *ChaosDRBDBlocker) Run(ctx context.Context) error {
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

func (c *ChaosDRBDBlocker) doBlock(ctx context.Context) error {
	// Get list of nodes from parent cluster
	nodes, err := c.parentClient.ListVMs(ctx)
	if err != nil {
		return err
	}

	if len(nodes) < 2 {
		c.log.Debug("not enough nodes for DRBD blocking", "node_count", len(nodes))
		return nil
	}

	// Select random pair of nodes
	nodeA, nodeB := c.randomNodePair(nodes)

	// Collect actual DRBD ports from the two selected nodes before blocking.
	// This ensures we block current ports even if new RVs/RVRs were created since startup.
	actualPorts, err := c.portCollector.CollectPortsFromAllNodes(ctx, []chaos.NodeInfo{nodeA, nodeB})
	if err != nil {
		c.log.Warn("failed to collect DRBD ports, skipping incident",
			"node_a", nodeA.Name,
			"node_b", nodeB.Name,
			"error", err,
		)
		return nil // Skip this incident, will retry on next cycle
	}

	// Skip incident if no DRBD ports found (no RVs/RVRs on these nodes yet)
	if len(actualPorts) == 0 {
		c.log.Debug("no DRBD ports found on selected nodes, skipping incident",
			"node_a", nodeA.Name,
			"node_b", nodeB.Name,
		)
		return nil // Skip, wait for RVs to be created by life-simulation
	}

	c.log.Info("blocking DRBD ports",
		"node_a", nodeA.Name,
		"node_a_ip", nodeA.IPAddress,
		"node_b", nodeB.Name,
		"node_b_ip", nodeB.IPAddress,
		"ports", actualPorts,
		"port_count", len(actualPorts),
	)

	policyName, err := c.ciliumManager.BlockDRBDPortsList(ctx, nodeA, nodeB, actualPorts)
	if err != nil {
		return err
	}

	// Wait for incident duration or context cancellation
	incidentDuration := randomDuration(c.cfg.IncidentDuration)
	c.log.Debug("keeping DRBD blocked", "duration", incidentDuration.String())

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
	c.log.Info("unblocking DRBD ports",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
		"policy", policyName,
	)

	if err := c.ciliumManager.UnblockTraffic(context.Background(), policyName); err != nil {
		c.log.Error("failed to unblock DRBD ports", "error", err)
	}

	return nil
}

func (c *ChaosDRBDBlocker) cleanup(policyName string) {
	c.log.Info("cleanup: unblocking DRBD ports", "policy", policyName)
	if err := c.ciliumManager.UnblockTraffic(context.Background(), policyName); err != nil {
		c.log.Error("cleanup failed", "error", err)
	}
}

func (c *ChaosDRBDBlocker) randomNodePair(nodes []chaos.NodeInfo) (chaos.NodeInfo, chaos.NodeInfo) {
	// Shuffle nodes
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	return nodes[0], nodes[1]
}
