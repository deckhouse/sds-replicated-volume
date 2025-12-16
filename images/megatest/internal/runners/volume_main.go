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
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	rvCreateTimeout = 1 * time.Minute
	rvDeleteTimeout = 1 * time.Minute
)

var (
	publisher1MinMax = []int{30, 60}
	publisher2MinMax = []int{100, 200}
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

	// Tracking running volumes
	runningSubRunners atomic.Int32
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
	numberOfPublishNodes := v.getRundomNumberForNodes()
	publishNodes, err := v.getPublishNodes(ctx, numberOfPublishNodes)
	if err != nil {
		v.log.Error("failed to get published nodes", "error", err)
		return err
	}
	v.log.Debug("published nodes", "nodes", publishNodes)

	// Create RV
	if err := v.createRV(ctx, publishNodes); err != nil {
		v.log.Error("failed to create RV", "error", err)
		return err
	}

	// Start all sub-runners immediately after RV creation
	// They will operate while we wait for Ready
	v.startSubRunners(lifetimeCtx)

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

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), rvDeleteTimeout)
	defer cleanupCancel()

	if err := v.deleteRV(cleanupCtx); err != nil {
		v.log.Error("failed to delete RV", "error", err)
	}

	for v.runningSubRunners.Load() > 0 {
		log.Info("waiting for sub-runners to stop", "remaining", v.runningSubRunners.Load())
		time.Sleep(1 * time.Second)
	}
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

func (v *VolumeMain) getPublishNodes(ctx context.Context, count int) ([]string, error) {
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

func (v *VolumeMain) createRV(ctx context.Context, publishNodes []string) error {
	// Ensure PublishOn is never nil (use empty slice instead)
	publishOn := publishNodes
	if publishOn == nil {
		publishOn = []string{}
	}

	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.rvName,
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			Size:                       v.initialSize,
			ReplicatedStorageClassName: v.storageClass,
			PublishOn:                  publishOn,
		},
	}

	err := v.client.CreateRV(ctx, rv)
	if err != nil {
		return err
	}

	return nil
}

func (v *VolumeMain) deleteRV(ctx context.Context) error {
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.rvName,
		},
	}

	err := v.client.DeleteRV(ctx, rv)
	if err != nil {
		return err
	}

	// TODO: Wait for deletion

	return nil
}

func (v *VolumeMain) startSubRunners(ctx context.Context) {
	// Create publisher configs
	publisher1Cfg := config.VolumePublisherConfig{
		Period: config.DurationMinMax{
			Min: time.Duration(publisher1MinMax[0]) * time.Second,
			Max: time.Duration(publisher1MinMax[1]) * time.Second,
		},
	}
	publisher2Cfg := config.VolumePublisherConfig{
		Period: config.DurationMinMax{
			Min: time.Duration(publisher2MinMax[0]) * time.Second,
			Max: time.Duration(publisher2MinMax[1]) * time.Second,
		},
	}

	// Create runners
	publishers := []*VolumePublisher{
		NewVolumePublisher(v.rvName, publisher1Cfg, v.client, publisher1MinMax),
		NewVolumePublisher(v.rvName, publisher2Cfg, v.client, publisher2MinMax),
	}

	// Start publishers
	for _, pub := range publishers {
		publisherCtx, cancel := context.WithCancel(ctx)
		go func(p *VolumePublisher) {
			v.runningSubRunners.Add(1)
			defer func() {
				cancel()
				v.runningSubRunners.Add(-1)
			}()

			_ = p.Run(publisherCtx)
		}(pub)
	}

	// Start resizer
	if v.disableVolumeResizer {
		v.log.Debug("volume-resizer runner is disabled")
	} else {
		v.log.Debug("volume-resizer runner is enabled")
	}

	// Start replica destroyer
	if v.disableVolumeReplicaDestroyer {
		v.log.Debug("volume-replica-destroyer runner is disabled")
	} else {
		v.log.Debug("volume-replica-destroyer runner is enabled")
	}

	// Start replica creator
	if v.disableVolumeReplicaCreator {
		v.log.Debug("volume-replica-creator runner is disabled")
	} else {
		v.log.Debug("volume-replica-creator runner is enabled")
	}
}
