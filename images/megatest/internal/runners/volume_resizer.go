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

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// VolumeResizer periodically increases the size of a ReplicatedVolume
type VolumeResizer struct {
	rvName string
	cfg    config.VolumeResizerConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewVolumeResizer creates a new VolumeResizer
func NewVolumeResizer(
	rvName string,
	cfg config.VolumeResizerConfig,
	client *kubeutils.Client,
	periodMinMax []int,
	stepMinMax []string,
) *VolumeResizer {
	return &VolumeResizer{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "volume-resizer", "rv_name", rvName, "period_min_max", periodMinMax, "step_min_max", stepMinMax),
	}
}

// Run starts the resize cycle until context is cancelled
func (v *VolumeResizer) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		// Wait random duration before resize
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform resize
		if err := v.doResize(ctx); err != nil {
			v.log.Error("resize failed", "error", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeResizer) doResize(ctx context.Context) error {
	v.log.Debug("resizing volume -------------------------------------")
	_ = ctx
	return nil
}
