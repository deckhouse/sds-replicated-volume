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

const (
	// publishCycleProbability is the probability of a publish cycle (vs unpublish)
	publishCycleProbability = 0.10
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

		// TODO: maybe it's necessary to collect time statistics by cycles?
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
			if !slices.Contains(rv.Spec.PublishOn, nodeName) {
				nodeName = rv.Spec.PublishOn[0]
			}
			if err := v.unpublishCycle(ctx, rv, nodeName); err != nil {
				log.Error("failed to unpublishCycle", "error", err, "case", 2)
				return err
			}
		default:
			err := fmt.Errorf("unexpected number of nodes in PublishOn: %d", len(rv.Spec.PublishOn))
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

	rv, err := v.client.GetRV(cleanupCtx, v.rvName)
	if err != nil {
		log.Error("failed to get RV for cleanup", "error", err)
		return
	}

	if err := v.unpublishCycle(cleanupCtx, rv, ""); err != nil {
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

	// Wait for node to be published
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

func (v *VolumePublisher) publishAndUnpublishCycle(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "publishAndUnpublishCycle")
	log.Debug("started")
	defer log.Debug("finished")

	// Step 1: Publish the node and wait for it to be published
	if err := v.publishCycle(ctx, rv, nodeName); err != nil {
		return err
	}

	// Step 2: Random delay between publish and unpublish
	randomDelay := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before unpublish", "duration", randomDelay.String())
	if err := waitWithContext(ctx, randomDelay); err != nil {
		return err
	}

	// Step 3: Get fresh RV and unpublish
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	return v.unpublishCycle(ctx, rv, nodeName)
}

func (v *VolumePublisher) migrationCycle(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "migrationCycle")
	log.Debug("started")
	defer log.Debug("finished")

	// Find the other node (not nodeName) from current PublishOn
	// In case 1, there should be exactly one node in PublishOn
	if len(rv.Spec.PublishOn) != 1 {
		return fmt.Errorf("expected exactly one node in PublishOn for migration, got %d", len(rv.Spec.PublishOn))
	}
	otherNodeName := rv.Spec.PublishOn[0]
	if otherNodeName == nodeName {
		return fmt.Errorf("other node name equals selected node name: %s", nodeName)
	}

	// Step 1: Publish the selected node
	if err := v.doPublish(ctx, rv, nodeName); err != nil {
		log.Error("failed to doPublish selected node", "error", err)
		return err
	}

	// Wait for both nodes to be published
	for {
		log.Debug("waiting for both nodes to be published", "selected_node", nodeName, "other_node", otherNodeName)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status != nil &&
			slices.Contains(rv.Status.PublishedOn, nodeName) &&
			slices.Contains(rv.Status.PublishedOn, otherNodeName) {
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Step 2: Random delay
	randomDelay1 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before unpublishing other node", "duration", randomDelay1.String())
	if err := waitWithContext(ctx, randomDelay1); err != nil {
		return err
	}

	// Step 3: Get fresh RV and unpublish the other node
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	if err := v.doUnpublish(ctx, rv, otherNodeName); err != nil {
		log.Error("failed to doUnpublish other node", "error", err, "other_node", otherNodeName)
		return err
	}

	// Wait for other node to be unpublished
	for {
		log.Debug("waiting for other node to be unpublished", "other_node", otherNodeName)

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status == nil || !slices.Contains(rv.Status.PublishedOn, otherNodeName) {
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Step 4: Random delay
	randomDelay2 := randomDuration(v.cfg.Period)
	log.Debug("waiting random delay before unpublishing selected node", "duration", randomDelay2.String())
	if err := waitWithContext(ctx, randomDelay2); err != nil {
		return err
	}

	// Step 5: Get fresh RV and unpublish the selected node
	rv, err = v.client.GetRV(ctx, v.rvName)
	if err != nil {
		return err
	}

	if err := v.doUnpublish(ctx, rv, nodeName); err != nil {
		log.Error("failed to doUnpublish selected node", "error", err)
		return err
	}

	// Wait for selected node to be unpublished
	for {
		log.Debug("waiting for selected node to be unpublished")

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status == nil || !slices.Contains(rv.Status.PublishedOn, nodeName) {
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

	originalRV := rv.DeepCopy()
	rv.Spec.PublishOn = append(rv.Spec.PublishOn, nodeName)

	err := v.client.PatchRV(ctx, originalRV, rv)
	if err != nil {
		return fmt.Errorf("failed to patch RV with new publish node: %w", err)
	}

	return nil
}

func (v *VolumePublisher) unpublishCycle(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	log := v.log.With("node_name", nodeName, "func", "unpublishCycle")
	log.Debug("started")
	defer log.Debug("finished")

	if err := v.doUnpublish(ctx, rv, nodeName); err != nil {
		log.Error("failed to doUnpublish", "error", err)
		return err
	}

	// Wait for node(s) to be unpublished
	for {
		if nodeName == "" {
			log.Debug("waiting for all nodes to be unpublished")
		} else {
			log.Debug("waiting for node to be unpublished")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			return err
		}

		if rv.Status == nil {
			// If status is nil, consider it as unpublished
			return nil
		}

		if nodeName == "" {
			// Check if all nodes are unpublished
			if len(rv.Status.PublishedOn) == 0 {
				return nil
			}
		} else {
			// Check if specific node is unpublished
			if !slices.Contains(rv.Status.PublishedOn, nodeName) {
				return nil
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumePublisher) doUnpublish(ctx context.Context, rv *v1alpha3.ReplicatedVolume, nodeName string) error {
	originalRV := rv.DeepCopy()

	if nodeName == "" {
		// Unpublish from all nodes - make PublishOn empty
		rv.Spec.PublishOn = []string{}
	} else {
		// Check if node is in PublishOn
		if !slices.Contains(rv.Spec.PublishOn, nodeName) {
			v.log.Debug("node not in PublishOn", "node_name", nodeName)
			return nil
		}

		// Remove node from PublishOn
		newPublishOn := make([]string, 0, len(rv.Spec.PublishOn))
		for _, node := range rv.Spec.PublishOn {
			if node != nodeName {
				newPublishOn = append(newPublishOn, node)
			}
		}
		rv.Spec.PublishOn = newPublishOn
	}

	err := v.client.PatchRV(ctx, originalRV, rv)
	if err != nil {
		return fmt.Errorf("failed to patch RV to unpublish node: %w", err)
	}

	return nil
}

func (v *VolumePublisher) isAPublishCycle() bool {
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	r := rand.Float64()
	return r < publishCycleProbability
}
