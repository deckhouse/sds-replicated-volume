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

const (
	// blockingEverythingProbability is the probability threshold for blocking-everything incident (30%)
	blockingEverythingProbability = 30
	// blockingDRBDProbability is the probability threshold for blocking-drbd incident (30%, cumulative: 60%)
	blockingDRBDProbability = 60
)

// ChaosNetworkBlocker periodically blocks network between nodes with different incident types
type ChaosNetworkBlocker struct {
	cfg              config.ChaosNetworkBlockerConfig
	networkBlockMgr  *chaos.NetworkBlockManager
	parentClient     *chaos.ParentClient
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewChaosNetworkBlocker creates a new ChaosNetworkBlocker
func NewChaosNetworkBlocker(
	cfg config.ChaosNetworkBlockerConfig,
	networkBlockMgr *chaos.NetworkBlockManager,
	parentClient *chaos.ParentClient,
	forceCleanupChan <-chan struct{},
) *ChaosNetworkBlocker {
	return &ChaosNetworkBlocker{
		cfg:              cfg,
		networkBlockMgr:  networkBlockMgr,
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
			// Don't log error if context was cancelled (normal shutdown)
			if ctx.Err() == context.Canceled {
				return err
			}
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

	// Shuffle nodes randomly
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	// Select incident type based on probability
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	randValue := rand.Intn(100)

	switch {
	case randValue < blockingEverythingProbability:
		// blocking-everything: 30% probability
		return c.doBlockingEverything(ctx, nodes)
	case randValue < blockingDRBDProbability:
		// blocking-drbd: 30% probability (cumulative: 30-60%)
		return c.doBlockingDRBD(ctx, nodes)
	default:
		// split-brain: 40% probability (60-100%)
		return c.doSplitBrain(ctx, nodes)
	}
}

// doBlockingEverything blocks all TCP traffic between two random nodes
func (c *ChaosNetworkBlocker) doBlockingEverything(ctx context.Context, nodes []chaos.NodeInfo) error {
	nodeA, nodeB := nodes[0], nodes[1]

	c.log.Info("blocking all network",
		"incident_type", "blocking-everything",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
	)

	policyName, err := c.networkBlockMgr.BlockAllNetwork(ctx, nodeA, nodeB)
	if err != nil {
		return err
	}

	// Wait for incident duration or context cancellation
	incidentDuration := randomDuration(c.cfg.IncidentDuration)
	c.log.Debug("keeping network blocked", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup([]string{policyName})
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup([]string{policyName})
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, unblock
	}

	// Unblock traffic
	c.log.Info("unblocking network",
		"incident_type", "blocking-everything",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
		"policy", policyName,
	)

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()
	if err := c.networkBlockMgr.UnblockTraffic(cleanupCtx, policyName); err != nil {
		c.log.Error("failed to unblock network", "error", err)
	}

	return nil
}

// doBlockingDRBD is a stub for blocking-drbd incident (implementation deferred)
func (c *ChaosNetworkBlocker) doBlockingDRBD(ctx context.Context, nodes []chaos.NodeInfo) error {
	nodeA, nodeB := nodes[0], nodes[1]

	c.log.Info("blocking-drbd incident selected (not implemented yet)",
		"incident_type", "blocking-drbd",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
	)

	return nil
}

// doSplitBrain creates a network partition by splitting nodes into two groups
func (c *ChaosNetworkBlocker) doSplitBrain(ctx context.Context, nodes []chaos.NodeInfo) error {
	// Split nodes into two groups
	groupA, groupB := c.splitNodes(nodes)

	c.log.Info("creating network partition",
		"incident_type", "split-brain",
		"group_a_size", len(groupA),
		"group_b_size", len(groupB),
		"group_a_nodes", nodeNames(groupA),
		"group_b_nodes", nodeNames(groupB),
	)

	// Create blocking policies between groups
	var policyNames []string
	for _, nodeA := range groupA {
		for _, nodeB := range groupB {
			policyName, err := c.networkBlockMgr.BlockAllNetwork(ctx, nodeA, nodeB)
			if err != nil {
				c.log.Error("failed to block network", "node_a", nodeA.Name, "node_b", nodeB.Name, "error", err)
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
	c.log.Info("removing network partition",
		"incident_type", "split-brain",
		"policy_count", len(policyNames),
	)
	c.cleanup(policyNames)

	return nil
}

func (c *ChaosNetworkBlocker) cleanup(policyNames []string) {
	c.log.Info("started cleanup")
	defer c.log.Info("finished cleanup")

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()

	for _, policyName := range policyNames {
		if err := c.networkBlockMgr.UnblockTraffic(cleanupCtx, policyName); err != nil {
			c.log.Error("cleanup failed", "policy", policyName, "error", err)
		}
	}
}

func (c *ChaosNetworkBlocker) splitNodes(nodes []chaos.NodeInfo) ([]chaos.NodeInfo, []chaos.NodeInfo) {
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
