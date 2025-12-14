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
	"math/rand"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	rvCreateTimeout = 10 * time.Minute
	rvDeleteTimeout = 10 * time.Minute
)

// VolumeMain manages the lifecycle of a single ReplicatedVolume and its sub-runners
type VolumeMain struct {
	rvName         string
	storageClass   string
	volumeLifetime time.Duration
	initialSize    resource.Quantity
	client         *kubeutils.Client
	log            *slog.Logger

	// Disable flags for sub-runners
	disableVolumeResizer          bool
	disableVolumeReplicaDestroyer bool
	disableVolumeReplicaCreator   bool
}

// NewVolumeMain creates a new VolumeMain
func NewVolumeMain(
	rvName string,
	cfg config.VolumeMainConfig,
	client *kubeutils.Client,
) *VolumeMain {
	return &VolumeMain{
		rvName:                        rvName,
		storageClass:                  cfg.StorageClassName,
		volumeLifetime:                cfg.VolumeLifetime,
		initialSize:                   cfg.InitialSize,
		client:                        client,
		log:                           slog.Default().With("runner", "volume-main", "rv_name", rvName, "storage_class", cfg.StorageClassName, "volume_lifetime", cfg.VolumeLifetime),
		disableVolumeResizer:          cfg.DisableVolumeResizer,
		disableVolumeReplicaDestroyer: cfg.DisableVolumeReplicaDestroyer,
		disableVolumeReplicaCreator:   cfg.DisableVolumeReplicaCreator,
	}
}

// Run executes the full lifecycle of a volume
func (v *VolumeMain) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	// Create lifetime context
	lifetimeCtx, lifetimeCancel := context.WithTimeout(ctx, v.volumeLifetime)
	defer lifetimeCancel()

	// Determine initial publish nodes (random distribution: 0=30%, 1=60%, 2=10%)
	numberOfPublishedNodes := v.getRundomNumberForNodes()
	publishedNodes, err := v.getPublishedNodes(ctx, numberOfPublishedNodes)
	if err != nil {
		v.log.Error("failed to get published nodes", "error", err)
		return err
	}
	v.log.Debug("published nodes", "nodes", publishedNodes)

	// Wait for lifetime to expire or context to be cancelled
	<-lifetimeCtx.Done()

	// Cleanup sequence
	v.cleanup(ctx, lifetimeCtx)

	return nil
}

func (v *VolumeMain) cleanup(ctx context.Context, lifetimeCtx context.Context) {
	reason := ctx.Err()
	if reason == nil {
		reason = lifetimeCtx.Err()
	}
	log := v.log.With("reason", reason, "func", "cleanup")
	log.Info("started")
	defer log.Info("finished")
}

func (v *VolumeMain) getRundomNumberForNodes() int {
	// 0 nodes = 30%, 1 node = 60%, 2 nodes = 10%
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	r := rand.Float64()
	switch {
	case r < 0.30:
		return 0
	case r < 0.90:
		return 1
	default:
		return 2
	}
}

func (v *VolumeMain) getPublishedNodes(ctx context.Context, count int) ([]string, error) {
	if count == 0 {
		return nil, nil
	}

	nodes, err := v.client.GetRandomNodes(ctx, count)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Name
	}
	return names, nil
}
