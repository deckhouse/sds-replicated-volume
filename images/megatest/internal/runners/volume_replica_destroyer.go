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

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// VolumeReplicaDestroyer periodically deletes random replicas from a volume.
// It does NOT wait for deletion to succeed.
type VolumeReplicaDestroyer struct {
	rvName string
	cfg    config.VolumeReplicaDestroyerConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewVolumeReplicaDestroyer creates a new VolumeReplicaDestroyer
func NewVolumeReplicaDestroyer(
	rvName string,
	cfg config.VolumeReplicaDestroyerConfig,
	client *kubeutils.Client,
	periodrMinMax []int,
) *VolumeReplicaDestroyer {
	return &VolumeReplicaDestroyer{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "volume-replica-destroyer", "rv_name", rvName, "period_min_max", periodrMinMax),
	}
}

// Run starts the destroy cycle until context is cancelled
func (v *VolumeReplicaDestroyer) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		// Wait random duration before delete
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform delete (errors are logged, not returned)
		v.doDestroy(ctx)
	}
}

func (v *VolumeReplicaDestroyer) doDestroy(ctx context.Context) {
	startTime := time.Now()

	// Get list of RVRs for this RV
	rvrs, err := v.client.ListRVRsByRVName(ctx, v.rvName)
	if err != nil {
		v.log.Error("failed to list RVRs", "error", err)
		return
	}

	if len(rvrs) == 0 {
		v.log.Debug("no RVRs found to destroy")
		return
	}

	// Select random RVR
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	idx := rand.Intn(len(rvrs))
	selectedRVR := &rvrs[idx]

	// Delete RVR (do NOT wait for success)
	if err := v.client.DeleteRVR(ctx, selectedRVR); err != nil {
		v.log.Error("failed to delete RVR",
			"rvr_name", selectedRVR.Name,
			"error", err)
		return
	}

	// Log success
	v.log.Info("RVR deleted",
		"rvr_name", selectedRVR.Name,
		"rvr_type", selectedRVR.Spec.Type,
		"rvr_node", selectedRVR.Spec.NodeName,
		"duration", time.Since(startTime))
}
