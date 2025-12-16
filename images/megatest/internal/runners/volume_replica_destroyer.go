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

// VolumeReplicaDestroyer periodically deletes random replicas from a volume
// It does NOT wait for deletion to succeed
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

		// Perform delete
		if err := v.doDestroy(ctx); err != nil {
			v.log.Error("destroy failed", "error", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeReplicaDestroyer) doDestroy(ctx context.Context) error {
	for i := 0; i < 5; i++ {
		v.log.Debug("destroying random replica", "attempt", i)
		time.Sleep(1 * time.Second)
	}

	return nil
}
