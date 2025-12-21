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
	"fmt"
	"log/slog"
	"math/rand"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
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
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			v.cleanup(err)
			return nil
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			v.log.Error("failed to get RV", "error", err)
			return err
		}

		// get a random node
		nodes, err := v.client.GetRandomNodes(ctx, 1)
		if err != nil {
			v.log.Error("failed to get random node", "error", err)
			return err
		}
		nodeName := nodes[0].Name
		v.log.Debug("random node", "node_name", nodeName)

		switch len(rv.Spec.PublishOn) {
		case 0:
			if v.isAPublishCycle() {
				if err := v.RunPublishCycle(ctx, nodeName); err != nil {
					return err
				}
			} else {
				if err := v.RunPublishAndUnpublishCycle(ctx, nodeName); err != nil {
					return err
				}
			}
		case 1:
			if slices.Contains(rv.Spec.PublishOn, nodeName) {
				if err := v.RunUnpublishCycle(ctx, nodeName); err != nil {
					return err
				}
			} else {
				if err := v.RunMigrationCycle(ctx, nodeName); err != nil {
					return err
				}
			}
		case 2:
			if err := v.RunUnpublishCycle(ctx, nodeName); err != nil {
				return err
			}
		default:
			err := fmt.Errorf("unexpected number of nodes in rv.Spec.PublishOn: %d", len(rv.Spec.PublishOn))
			v.log.Error("error", "error", err)
			return err
		}
	}
}

func (v *VolumePublisher) cleanup(reason error) {
	log := v.log.With("reason", reason, "func", "cleanup")
	log.Info("started")
	defer log.Info("finished")

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()

	if err := v.doUnpublish(cleanupCtx); err != nil {
		v.log.Error("failed to unpublish", "error", err)
	}
}

func (v *VolumePublisher) doPublish(ctx context.Context) error {
	v.log.Debug("publishing to random node")
	// TODO: Wait for publish success

	return nil
}

func (v *VolumePublisher) doUnpublish(ctx context.Context) error {
	v.log.Debug("unpublishing from random node")
	// TODO: Wait for unpublish success

	return nil
}

func (v *VolumePublisher) isAPublishCycle() bool {
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	r := rand.Float64()
	switch {
	case r < 0.10:
		return true
	default:
		return false
	}
}
