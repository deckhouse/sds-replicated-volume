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
	// minRebootPeriod is the minimum period between reboots (5 minutes)
	minRebootPeriod = 5 * time.Minute
	// minRebootPeriodMax is the maximum period when minRebootPeriod is enforced (6 minutes)
	minRebootPeriodMax = 6 * time.Minute
)

// ChaosVMReboter periodically performs hard reboot of random VMs
type ChaosVMReboter struct {
	cfg          config.ChaosVMReboterConfig
	parentClient *chaos.ParentClient
	log          *slog.Logger
}

// NewChaosVMReboter creates a new ChaosVMReboter
func NewChaosVMReboter(
	cfg config.ChaosVMReboterConfig,
	parentClient *chaos.ParentClient,
) *ChaosVMReboter {
	return &ChaosVMReboter{
		cfg:          cfg,
		parentClient: parentClient,
		log:          slog.Default().With("runner", "chaos-vm-reboter"),
	}
}

// Run starts the VM reboot cycle until context is cancelled
func (c *ChaosVMReboter) Run(ctx context.Context) error {
	c.log.Info("started")
	defer c.log.Info("finished")

	for {
		// Determine wait period
		period := randomDuration(c.cfg.Period)

		// If period is less than 5 minutes, use 5-6 minutes instead
		if period < minRebootPeriod {
			period = randomDuration(config.DurationMinMax{
				Min: minRebootPeriod,
				Max: minRebootPeriodMax,
			})
			c.log.Debug("period adjusted to minimum", "original_period", c.cfg.Period, "adjusted_period", period)
		}

		// Wait for period
		if err := waitWithContext(ctx, period); err != nil {
			return err
		}

		// Perform reboot
		if err := c.doReboot(ctx); err != nil {
			c.log.Error("reboot failed", "error", err)
		}
	}
}

func (c *ChaosVMReboter) doReboot(ctx context.Context) error {
	// Get list of VMs from parent cluster
	nodes, err := c.parentClient.ListVMs(ctx)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		c.log.Debug("no VMs available for reboot")
		return nil
	}

	// Shuffle nodes randomly
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	// Try to find a VM without unfinished operations
	var selectedVM *chaos.NodeInfo
	for _, node := range nodes {
		hasUnfinished, err := c.parentClient.HasUnfinishedVMOperations(ctx, node.Name)
		if err != nil {
			c.log.Warn("failed to check VM operations", "vm", node.Name, "error", err)
			continue
		}

		if !hasUnfinished {
			selectedVM = &node
			break
		}

		c.log.Debug("skipping VM with unfinished operations", "vm", node.Name)
	}

	// If no VM without unfinished operations found, use first one and log warning
	if selectedVM == nil {
		selectedVM = &nodes[0]
		c.log.Warn("no VM without unfinished operations found, using first VM anyway",
			"vm", selectedVM.Name,
		)
	}

	vm := *selectedVM

	// Check again for unfinished operations before creating new one
	hasUnfinished, err := c.parentClient.HasUnfinishedVMOperations(ctx, vm.Name)
	if err != nil {
		c.log.Warn("failed to check VM operations before reboot", "vm", vm.Name, "error", err)
		// Continue anyway
	} else if hasUnfinished {
		c.log.Info("skipping reboot, VM has unfinished operations",
			"vm", vm.Name,
		)
		return nil
	}

	c.log.Info("performing hard reboot",
		"vm", vm.Name,
		"ip", vm.IPAddress,
		"force", true,
	)

	// Create VirtualMachineOperation for hard reboot
	if err := c.parentClient.CreateVMOperation(ctx, vm.Name, chaos.VMOperationRestart, true); err != nil {
		return err
	}

	c.log.Info("VM reboot initiated",
		"vm", vm.Name,
	)

	return nil
}

func (c *ChaosVMReboter) randomVM(nodes []chaos.NodeInfo) chaos.NodeInfo {
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	return nodes[rand.Intn(len(nodes))]
}
