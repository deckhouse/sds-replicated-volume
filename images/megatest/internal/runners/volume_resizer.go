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
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

const (
	resizeWaitTimeout = 5 * time.Minute
)

// VolumeResizer periodically increases the size of a ReplicatedVolume
type VolumeResizer struct {
	rvName string
	cfg    config.VolumeResizerConfig
	client *k8sclient.Client
	log    *logging.Logger
}

// NewVolumeResizer creates a new VolumeResizer
func NewVolumeResizer(rvName string, cfg config.VolumeResizerConfig, client *k8sclient.Client) *VolumeResizer {
	return &VolumeResizer{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    logging.NewLogger(rvName, "volume-resizer"),
	}
}

// Name returns the runner name
func (v *VolumeResizer) Name() string {
	return "volume-resizer"
}

// Run starts the resize cycle until context is cancelled
func (v *VolumeResizer) Run(ctx context.Context) error {
	v.log.Info("starting volume resizer")
	defer v.log.Info("volume resizer stopped")

	for {
		// Wait random duration before resize
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform resize
		if err := v.doResize(ctx); err != nil {
			v.log.Error("resize failed", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeResizer) doResize(ctx context.Context) error {
	// Get current RV
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	// Calculate new size: current + random step
	step := randomSize(v.cfg.Step)
	currentSize := rv.Spec.Size
	newSizeValue := currentSize.Value() + step.Value()
	newSize := *resource.NewQuantity(newSizeValue, resource.BinarySI)

	params := logging.ActionParams{
		"current_size": currentSize.String(),
		"step":         step.String(),
		"new_size":     newSize.String(),
	}

	v.log.ActionStarted("resize", params)
	startTime := time.Now()

	// Update RV size
	err = v.client.ResizeRV(ctx, v.rvName, newSize)
	if err != nil {
		v.log.ActionFailed("resize", params, err, time.Since(startTime))
		return err
	}

	// Wait for resize to complete
	err = v.client.WaitForResize(ctx, v.rvName, newSize, resizeWaitTimeout)
	if err != nil {
		v.log.ActionFailed("resize", params, err, time.Since(startTime))
		return err
	}

	v.log.ActionCompleted("resize", params, "success", time.Since(startTime))
	return nil
}
