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
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	unpublishTimeout = 1 * time.Minute
)

// VolumePublisher periodically publishes and unpublishes a volume to random nodes
type VolumePublisher struct {
	rvName string
	cfg    config.VolumePublisherConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewVolumePublisher creates a new VolumePublisher
func NewVolumePublisher(rvName string, cfg config.VolumePublisherConfig, client *kubeutils.Client, periodrMinMax []int) *VolumePublisher {
	return &VolumePublisher{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "volume-publisher", "rv_name", rvName, "period_min_max", periodrMinMax),
	}
}

// Run starts the publish/unpublish cycle until context is cancelled
func (v *VolumePublisher) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		// Wait random duration before publish
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			v.cleanup(err)
			return nil
		}

		// Publish to random node
		if err := v.doPublish(ctx); err != nil {
			v.log.Error("publish failed", "error", err)
			// Continue even on failure
		}

		// Wait random duration before unpublish
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			v.cleanup(err)
			return nil
		}

		// Unpublish
		if err := v.doUnpublish(ctx); err != nil {
			v.log.Error("unpublish failed", "error", err)
			// Continue even on failure
		}
	}
}

func (v *VolumePublisher) cleanup(reason error) {
	log := v.log.With("reason", reason, "func", "cleanup")
	log.Info("started")
	defer log.Info("finished")

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), unpublishTimeout)
	defer cleanupCancel()

	if err := v.doUnpublish(cleanupCtx); err != nil {
		v.log.Error("failed to unpublish", "error", err)
	}
}

func (v *VolumePublisher) doPublish(ctx context.Context) error {
	v.log.Debug("publishing to random node")
	return nil
}

func (v *VolumePublisher) doUnpublish(ctx context.Context) error {
	v.log.Debug("unpublishing from random node")
	return nil
}
