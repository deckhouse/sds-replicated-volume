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
	"math/rand"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

// VolumeReplicaDestroyer periodically deletes random replicas from a volume
// It does NOT wait for deletion to succeed
type VolumeReplicaDestroyer struct {
	rvName     string
	cfg        config.VolumeReplicaDestroyerConfig
	client     *k8sclient.Client
	log        *logging.Logger
	instanceID string
	useV3API   bool // Use v1alpha3 API
}

// NewVolumeReplicaDestroyer creates a new VolumeReplicaDestroyer
func NewVolumeReplicaDestroyer(
	rvName string,
	cfg config.VolumeReplicaDestroyerConfig,
	client *k8sclient.Client,
	instanceID string,
) *VolumeReplicaDestroyer {
	return &VolumeReplicaDestroyer{
		rvName:     rvName,
		cfg:        cfg,
		client:     client,
		log:        logging.NewLogger(rvName, "volume-replica-destroyer", instanceID),
		instanceID: instanceID,
		useV3API:   true, // Default to v1alpha3 API
	}
}

// Name returns the runner name
func (v *VolumeReplicaDestroyer) Name() string {
	return "volume-replica-destroyer"
}

// Run starts the destroy cycle until context is cancelled
func (v *VolumeReplicaDestroyer) Run(ctx context.Context) error {
	v.log.Info("starting volume replica destroyer")
	defer v.log.Info("volume replica destroyer stopped")

	for {
		// Wait random duration before delete
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform delete
		if err := v.doDestroy(ctx); err != nil {
			v.log.Error("destroy failed", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeReplicaDestroyer) doDestroy(ctx context.Context) error {
	if v.useV3API {
		return v.doDestroyV3(ctx)
	}
	return v.doDestroyV2(ctx)
}

func (v *VolumeReplicaDestroyer) doDestroyV3(ctx context.Context) error {
	// List replicas for this volume using v1alpha3
	rvrs, err := v.client.ListRVRsForVolumev3(ctx, v.rvName)
	if err != nil {
		return err
	}

	if len(rvrs) == 0 {
		v.log.Info("no replicas to destroy")
		return nil
	}

	// Select random replica
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	selectedRVR := rvrs[rand.Intn(len(rvrs))]

	params := logging.ActionParams{
		"rvr_name":  selectedRVR.Name,
		"node_name": selectedRVR.Spec.NodeName,
		"type":      selectedRVR.Spec.Type,
	}

	v.log.ActionStarted("destroy_replica", params)
	startTime := time.Now()

	// Delete the RVR (don't wait for completion)
	err = v.client.DeleteRVRv3(ctx, selectedRVR.Name)
	if err != nil {
		v.log.ActionFailed("destroy_replica", params, err, time.Since(startTime))
		return fmt.Errorf("deleting RVR %s: %w", selectedRVR.Name, err)
	}

	// Log completion immediately (fire and forget)
	v.log.ActionCompleted("destroy_replica", params, "delete_initiated", time.Since(startTime))
	return nil
}

func (v *VolumeReplicaDestroyer) doDestroyV2(ctx context.Context) error {
	// List replicas for this volume using v1alpha2
	rvrs, err := v.client.ListRVRsForVolume(ctx, v.rvName)
	if err != nil {
		return err
	}

	if len(rvrs) == 0 {
		v.log.Info("no replicas to destroy")
		return nil
	}

	// Select random replica
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	selectedRVR := rvrs[rand.Intn(len(rvrs))]

	params := logging.ActionParams{
		"rvr_name":  selectedRVR.Name,
		"node_name": selectedRVR.Spec.NodeName,
	}

	v.log.ActionStarted("destroy_replica", params)
	startTime := time.Now()

	// Delete the RVR (don't wait for completion)
	err = v.client.DeleteRVR(ctx, selectedRVR.Name)
	if err != nil {
		v.log.ActionFailed("destroy_replica", params, err, time.Since(startTime))
		return fmt.Errorf("deleting RVR %s: %w", selectedRVR.Name, err)
	}

	// Log completion immediately (fire and forget)
	v.log.ActionCompleted("destroy_replica", params, "delete_initiated", time.Since(startTime))
	return nil
}

