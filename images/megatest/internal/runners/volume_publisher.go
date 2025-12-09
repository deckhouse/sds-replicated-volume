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
	"slices"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

const (
	publishWaitTimeout = 5 * time.Minute
)

// VolumePublisher periodically publishes and unpublishes a volume to random nodes
type VolumePublisher struct {
	rvName     string
	cfg        config.VolumePublisherConfig
	client     *k8sclient.Client
	log        *logging.Logger
	instanceID string

	// Track current published node for this publisher
	currentNode string
}

// NewVolumePublisher creates a new VolumePublisher
func NewVolumePublisher(rvName string, cfg config.VolumePublisherConfig, client *k8sclient.Client, instanceID string) *VolumePublisher {
	return &VolumePublisher{
		rvName:     rvName,
		cfg:        cfg,
		client:     client,
		log:        logging.NewLogger(rvName, "volume-publisher", instanceID),
		instanceID: instanceID,
	}
}

// Name returns the runner name
func (v *VolumePublisher) Name() string {
	return "volume-publisher"
}

// Run starts the publish/unpublish cycle until context is cancelled
func (v *VolumePublisher) Run(ctx context.Context) error {
	v.log.Info("starting volume publisher")
	defer v.log.Info("volume publisher stopped")

	for {
		// Wait random duration before publish
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			// Context cancelled, do unpublish before exit
			v.doUnpublish(context.Background())
			return nil
		}

		// Publish to random node
		if err := v.doPublish(ctx); err != nil {
			v.log.Error("publish failed", err)
			// Continue even on failure
		}

		// Wait random duration before unpublish
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			// Context cancelled, do unpublish before exit
			v.doUnpublish(context.Background())
			return nil
		}

		// Unpublish
		if err := v.doUnpublish(ctx); err != nil {
			v.log.Error("unpublish failed", err)
			// Continue even on failure
		}
	}
}

func (v *VolumePublisher) doPublish(ctx context.Context) error {
	// Select random node
	node, err := v.client.SelectRandomNode(ctx)
	if err != nil {
		return err
	}
	nodeName := node.Name

	params := logging.ActionParams{
		"node": nodeName,
	}

	v.log.ActionStarted("publish", params)
	startTime := time.Now()

	// Get current RV to read existing publishRequested
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		v.log.ActionFailed("publish", params, err, time.Since(startTime))
		return err
	}

	// Build new publishRequested list: keep existing but add our node
	// Max 2 nodes allowed
	newPublishRequested := rv.Spec.PublishRequested
	if !slices.Contains(newPublishRequested, nodeName) {
		if len(newPublishRequested) >= 2 {
			// Remove one to make room (prefer removing our previous node if any)
			if v.currentNode != "" {
				idx := slices.Index(newPublishRequested, v.currentNode)
				if idx >= 0 {
					newPublishRequested = slices.Delete(newPublishRequested, idx, idx+1)
				} else {
					// Remove first
					newPublishRequested = newPublishRequested[1:]
				}
			} else {
				newPublishRequested = newPublishRequested[1:]
			}
		}
		newPublishRequested = append(newPublishRequested, nodeName)
	}

	// Update publishRequested
	err = v.client.SetPublishOn(ctx, v.rvName, newPublishRequested)
	if err != nil {
		v.log.ActionFailed("publish", params, err, time.Since(startTime))
		return err
	}

	// Wait for publish to be provided
	err = v.client.WaitForPublishProvided(ctx, v.rvName, []string{nodeName}, publishWaitTimeout)
	if err != nil {
		v.log.ActionFailed("publish", params, err, time.Since(startTime))
		return err
	}

	v.currentNode = nodeName
	v.log.ActionCompleted("publish", params, "success", time.Since(startTime))
	return nil
}

func (v *VolumePublisher) doUnpublish(ctx context.Context) error {
	if v.currentNode == "" {
		return nil // Nothing to unpublish
	}

	params := logging.ActionParams{
		"node": v.currentNode,
	}

	v.log.ActionStarted("unpublish", params)
	startTime := time.Now()

	// Get current RV
	rv, err := v.client.GetRV(ctx, v.rvName)
	if err != nil {
		v.log.ActionFailed("unpublish", params, err, time.Since(startTime))
		return err
	}

	// Remove our node from publishRequested
	newPublishRequested := make([]string, 0, len(rv.Spec.PublishRequested))
	for _, n := range rv.Spec.PublishRequested {
		if n != v.currentNode {
			newPublishRequested = append(newPublishRequested, n)
		}
	}

	// Update publishRequested
	err = v.client.SetPublishOn(ctx, v.rvName, newPublishRequested)
	if err != nil {
		v.log.ActionFailed("unpublish", params, err, time.Since(startTime))
		return err
	}

	// Wait for unpublish to complete
	err = v.client.WaitForPublishRemoved(ctx, v.rvName, []string{v.currentNode}, publishWaitTimeout)
	if err != nil {
		v.log.ActionFailed("unpublish", params, err, time.Since(startTime))
		return err
	}

	v.log.ActionCompleted("unpublish", params, "success", time.Since(startTime))
	v.currentNode = ""
	return nil
}

