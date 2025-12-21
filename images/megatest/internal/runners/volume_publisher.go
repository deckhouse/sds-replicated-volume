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
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
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
		log := v.log.With("node_name", nodeName)

		switch len(rv.Spec.PublishOn) {
		case 0:
			if v.isAPublishCycle() {
				if err := v.publishCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to publishCycle", "error", err, "case", 0)
					return err
				}
			} else {
				if err := v.publishAndUnpublishCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to publishAndUnpublishCycle", "error", err, "case", 0)
					return err
				}
			}
		case 1:
			if slices.Contains(rv.Spec.PublishOn, nodeName) {
				if err := v.unpublishCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to unpublishCycle", "error", err, "case", 1)
					return err
				}
			} else {
				if err := v.migrationCycle(ctx, rv, nodeName); err != nil {
					log.Error("failed to migrationCycle", "error", err, "case", 1)
					return err
				}
			}
		case 2:
			if err := v.unpublishCycle(ctx, rv, nodeName); err != nil {
				log.Error("failed to unpublishCycle", "error", err, "case", 2)
				return err
			}
		default:
			err := fmt.Errorf("unexpected number of nodes in rv.Spec.PublishOn: %d", len(rv.Spec.PublishOn))
			log.Error("error", "error", err)
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

	if err := v.unpublishCycle(cleanupCtx, ""); err != nil {
		v.log.Error("failed to unpublishCycle", "error", err)
	}
}

func (v *VolumePublisher) publishCycle(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "publishCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doPublish(ctx, rv, nodeName); err != nil {
		log.Error("failed to doPublish", "error", err)
		return err
	}

	for {
		log.Debug("waiting for node to be published")

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status != nil && slices.Contains(rv.Status.PublishedOn, nodeName) {
			return nil
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumePublisher) doPublish(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	// Check if node is already in PublishOn
	if slices.Contains(rv.Spec.PublishOn, nodeName) {
		v.log.Debug("node already in PublishOn", "node_name", nodeName)
		return nil
	}

	originalRv := rv.DeepCopy()
	rv.Spec.PublishOn = append(rv.Spec.PublishOn, nodeName)

	err := v.client.PatchRV(ctx, originalRv, rv)
	if err != nil {
		return fmt.Errorf("failed to patch RV with new publish node: %w", err)
	}

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
