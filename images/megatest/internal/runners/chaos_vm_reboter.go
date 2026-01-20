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
		// Wait random duration before next reboot
		if err := waitRandomWithContext(ctx, c.cfg.Period); err != nil {
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

	// Select random VM
	vm := c.randomVM(nodes)

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
