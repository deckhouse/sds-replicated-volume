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

// ChaosNetworkDegrader periodically applies network degradation (latency/packet loss) between random node pairs
type ChaosNetworkDegrader struct {
	cfg              config.ChaosNetworkDegraderConfig
	netemManager     *chaos.NetemManager
	parentClient     *chaos.ParentClient
	log              *slog.Logger
	forceCleanupChan <-chan struct{}
}

// NewChaosNetworkDegrader creates a new ChaosNetworkDegrader
func NewChaosNetworkDegrader(
	cfg config.ChaosNetworkDegraderConfig,
	netemManager *chaos.NetemManager,
	parentClient *chaos.ParentClient,
	forceCleanupChan <-chan struct{},
) *ChaosNetworkDegrader {
	return &ChaosNetworkDegrader{
		cfg:              cfg,
		netemManager:     netemManager,
		parentClient:     parentClient,
		forceCleanupChan: forceCleanupChan,
		log: slog.Default().With(
			"runner", "chaos-network-degrader",
			"delay_ms", cfg.DelayMs,
			"loss_percent", cfg.LossPercent,
			"rate_mbit", cfg.RateMbit,
		),
	}
}

// Run starts the network degradation cycle until context is cancelled
func (c *ChaosNetworkDegrader) Run(ctx context.Context) error {
	c.log.Info("started")
	defer c.log.Info("finished")

	for {
		// Wait random duration before next incident
		if err := waitRandomWithContext(ctx, c.cfg.Period); err != nil {
			return err
		}

		// Perform degradation
		if err := c.doDegrade(ctx); err != nil {
			c.log.Error("degradation failed", "error", err)
		}
	}
}

func (c *ChaosNetworkDegrader) doDegrade(ctx context.Context) error {
	// Get list of nodes from parent cluster
	nodes, err := c.parentClient.ListVMs(ctx)
	if err != nil {
		return err
	}

	if len(nodes) < 2 {
		c.log.Debug("not enough nodes for network degradation", "node_count", len(nodes))
		return nil
	}

	// Select random pair of nodes
	nodeA, nodeB := c.randomNodePair(nodes)

	// Determine incident duration first (needed for auto-cleanup timeout in job)
	incidentDuration := randomDuration(c.cfg.IncidentDuration)

	// Determine degradation parameters
	delayMs := randomInt(c.cfg.DelayMs.Min, c.cfg.DelayMs.Max)
	delayJitter := delayMs / 4 // 25% jitter
	if delayJitter < 1 {
		delayJitter = 1
	}
	lossPercent := randomFloat64(c.cfg.LossPercent.Min, c.cfg.LossPercent.Max)

	// Rate limit: 0 means no limit
	rateMbit := 0
	if c.cfg.RateMbit.Max > 0 {
		rateMbit = randomInt(c.cfg.RateMbit.Min, c.cfg.RateMbit.Max)
	}

	degradeCfg := chaos.NetworkDegradationConfig{
		DelayMs:          delayMs,
		DelayJitter:      delayJitter,
		LossPercent:      lossPercent,
		RateMbit:         rateMbit,
		IncidentDuration: incidentDuration, // For auto-cleanup timeout in job
	}

	c.log.Info("applying network degradation",
		"node_a", nodeA.Name,
		"target_ip", nodeB.IPAddress,
		"delay_ms", delayMs,
		"delay_jitter", delayJitter,
		"loss_percent", lossPercent,
		"rate_mbit", rateMbit,
		"duration", incidentDuration.String(),
	)

	// Apply degradation via netem Job
	// Note: Degradation is applied only from nodeA to nodeB (one-directional).
	// This is intentional for DRBD testing - even one-sided network issues
	// (latency/packet loss) will break replication and trigger failover scenarios.
	// Bidirectional degradation would require creating two Jobs, which is more complex
	// and not necessary for our testing purposes.
	jobName, err := c.netemManager.ApplyNetworkDegradation(ctx, nodeA.Name, nodeB.IPAddress, degradeCfg)
	if err != nil {
		return err
	}

	// Wait for incident duration or context cancellation
	c.log.Debug("keeping network degraded", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup(nodeA.Name, jobName)
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup(nodeA.Name, jobName)
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, remove degradation
	}

	// Remove degradation
	c.log.Info("removing network degradation",
		"node_a", nodeA.Name,
		"job", jobName,
	)

	if err := c.netemManager.RemoveNetworkDegradation(context.Background(), nodeA.Name); err != nil {
		c.log.Error("failed to remove network degradation", "error", err)
	}

	return nil
}

func (c *ChaosNetworkDegrader) cleanup(nodeName, jobName string) {
	c.log.Info("cleanup: removing network degradation", "node", nodeName, "job", jobName)
	if err := c.netemManager.RemoveNetworkDegradation(context.Background(), nodeName); err != nil {
		c.log.Error("cleanup failed", "error", err)
	}
}

func (c *ChaosNetworkDegrader) randomNodePair(nodes []chaos.NodeInfo) (chaos.NodeInfo, chaos.NodeInfo) {
	// Shuffle nodes
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	return nodes[0], nodes[1]
}
