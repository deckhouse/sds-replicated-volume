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
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/chaos"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
)

const (
	// lossesProbability is the probability threshold for losses incident (50%)
	lossesProbability = 50
)

// ChaosNetworkDegrader periodically applies network degradation (latency/packet loss) between random node pairs
type ChaosNetworkDegrader struct {
	cfg               config.ChaosNetworkDegraderConfig
	networkDegradeMgr *chaos.NetworkDegradeManager
	parentClient      *chaos.ParentClient
	log               *slog.Logger
	forceCleanupChan  <-chan struct{}
	activeJobNames    []string
}

// NewChaosNetworkDegrader creates a new ChaosNetworkDegrader
func NewChaosNetworkDegrader(
	cfg config.ChaosNetworkDegraderConfig,
	networkDegradeMgr *chaos.NetworkDegradeManager,
	parentClient *chaos.ParentClient,
	forceCleanupChan <-chan struct{},
) *ChaosNetworkDegrader {
	return &ChaosNetworkDegrader{
		cfg:               cfg,
		networkDegradeMgr: networkDegradeMgr,
		parentClient:      parentClient,
		forceCleanupChan:  forceCleanupChan,
		log: slog.Default().With(
			"runner", "chaos-network-degrader",
			"loss_percent", cfg.LossPercent,
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
			// Don't log error if context was cancelled (normal shutdown)
			if ctx.Err() == context.Canceled {
				return err
			}
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

	// Shuffle nodes randomly
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	// Select first two nodes
	nodeA, nodeB := nodes[0], nodes[1]

	// Determine incident duration
	incidentDuration := randomDuration(c.cfg.IncidentDuration)

	// Select incident type based on probability
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	randValue := rand.Intn(100)

	if randValue < lossesProbability {
		// losses: 50% probability
		return c.doLosses(ctx, nodeA, nodeB, incidentDuration)
	}

	// latency: 50% probability
	return c.doLatency(ctx, nodeA, nodeB, incidentDuration)
}

// doLosses applies packet loss using iptables
func (c *ChaosNetworkDegrader) doLosses(ctx context.Context, nodeA, nodeB chaos.NodeInfo, incidentDuration time.Duration) error {
	log := c.log.With(
		"incident_type", "losses",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
		"loss_percent", c.cfg.LossPercent,
		"duration", incidentDuration.String(),
	)

	log.Info("applying packet loss")

	jobNames, err := c.networkDegradeMgr.ApplyPacketLoss(ctx, nodeA, nodeB, c.cfg.LossPercent, incidentDuration)
	if err != nil {
		return err
	}

	c.activeJobNames = jobNames

	// Wait for incident duration or context cancellation
	log.Debug("keeping packet loss active", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup()
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup()
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, remove degradation
	}

	// Remove degradation
	log.Info("removing packet loss")
	c.cleanup()

	return nil
}

// doLatency applies latency using iperf3
func (c *ChaosNetworkDegrader) doLatency(ctx context.Context, nodeA, nodeB chaos.NodeInfo, incidentDuration time.Duration) error {
	log := c.log.With(
		"incident_type", "latency",
		"node_a", nodeA.Name,
		"node_b", nodeB.Name,
		"duration", incidentDuration.String(),
	)

	log.Info("applying latency")

	jobNames, err := c.networkDegradeMgr.ApplyLatency(ctx, nodeA, nodeB, incidentDuration)
	if err != nil {
		return err
	}

	c.activeJobNames = jobNames

	// Wait for incident duration or context cancellation
	log.Debug("keeping latency active", "duration", incidentDuration.String())

	select {
	case <-ctx.Done():
		// Cleanup on context cancellation
		c.cleanup()
		return ctx.Err()
	case <-c.forceCleanupChan:
		// Cleanup on force signal
		c.cleanup()
		return nil
	case <-waitChan(incidentDuration):
		// Normal timeout, remove degradation
	}

	// Remove degradation
	log.Info("removing latency")
	c.cleanup()

	return nil
}

func (c *ChaosNetworkDegrader) cleanup() {
	c.log.Info("started cleanup")
	defer c.log.Info("finished cleanup")

	if len(c.activeJobNames) == 0 {
		return
	}

	c.log.Info("cleanup: removing network degradation", "job_count", len(c.activeJobNames))
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()
	if err := c.networkDegradeMgr.RemoveNetworkDegradation(cleanupCtx, c.activeJobNames); err != nil {
		c.log.Error("cleanup failed", "error", err)
	}
	c.activeJobNames = nil
}
