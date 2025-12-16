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
type VolumeReplicaCreator struct {
	rvName string
	cfg    config.VolumeReplicaCreatorConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewVolumeReplicaDestroyer creates a new VolumeReplicaDestroyer
func NewVolumeReplicaCreator(
	rvName string,
	cfg config.VolumeReplicaCreatorConfig,
	client *kubeutils.Client,
	periodrMinMax []int,
) *VolumeReplicaCreator {
	return &VolumeReplicaCreator{
		rvName: rvName,
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "volume-replica-creator", "rv_name", rvName, "period_min_max", periodrMinMax),
	}
}

// Run starts the destroy cycle until context is cancelled
func (v *VolumeReplicaCreator) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	for {
		// Wait random duration before delete
		if err := waitRandomWithContext(ctx, v.cfg.Period); err != nil {
			return nil
		}

		// Perform delete
		if err := v.doCreate(ctx); err != nil {
			v.log.Error("destroy failed", "error", err)
			// Continue even on failure
		}
	}
}

func (v *VolumeReplicaCreator) doCreate(ctx context.Context) error {
	for i := 0; i < 5; i++ {
		v.log.Debug("creating random replica", "attempt", i)
		time.Sleep(1 * time.Second)
	}

	return nil
}
