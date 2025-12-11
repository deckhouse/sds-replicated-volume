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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

const (
	rvCreateTimeout = 10 * time.Minute
	rvDeleteTimeout = 10 * time.Minute
)

// VolumeMain manages the lifecycle of a single ReplicatedVolume and its sub-runners
type VolumeMain struct {
	rvName         string
	storageClass   string
	lifetimePeriod time.Duration
	initialSize    resource.Quantity
	client         *k8sclient.Client
	log            *logging.Logger

	// Disable flags for sub-runners
	disableVolumeResizer          bool
	disableVolumeReplicaDestroyer bool
	disableVolumeReplicaCreator   bool

	// Sub-runners
	checker          *VolumeChecker
	publishers       []*VolumePublisher
	resizer          *VolumeResizer
	replicaDestroyer *VolumeReplicaDestroyer
	replicaCreator   *VolumeReplicaCreator

	// Cancel functions for sub-runners
	subRunnersMu     sync.Mutex
	subRunnersCancel []context.CancelFunc
}

// NewVolumeMain creates a new VolumeMain
func NewVolumeMain(
	rvName string,
	cfg config.VolumeMainConfig,
	client *k8sclient.Client,
) *VolumeMain {
	return &VolumeMain{
		rvName:                        rvName,
		storageClass:                  cfg.StorageClassName,
		lifetimePeriod:                cfg.LifetimePeriod,
		initialSize:                   cfg.InitialSize,
		client:                        client,
		log:                           logging.NewLogger(rvName, "volume-main"),
		disableVolumeResizer:          cfg.DisableVolumeResizer,
		disableVolumeReplicaDestroyer: cfg.DisableVolumeReplicaDestroyer,
		disableVolumeReplicaCreator:   cfg.DisableVolumeReplicaCreator,
	}
}

// Name returns the runner name
func (v *VolumeMain) Name() string {
	return "volume-main"
}

// Run executes the full lifecycle of a volume
func (v *VolumeMain) Run(ctx context.Context) error {
	v.log.Info("starting volume main")
	defer v.log.Info("volume main stopped")

	// Create lifetime context
	lifetimeCtx, lifetimeCancel := context.WithTimeout(ctx, v.lifetimePeriod)
	defer lifetimeCancel()

	// Determine initial publish nodes (random distribution: 0=30%, 1=60%, 2=10%)
	initialPublishCount := v.selectInitialPublishCount()
	initialNodes, err := v.selectInitialNodes(ctx, initialPublishCount)
	if err != nil {
		v.log.Error("failed to select initial nodes", err)
		return err
	}

	// Create RV
	if err := v.createRV(ctx, initialNodes); err != nil {
		return err
	}

	// Start all sub-runners immediately after RV creation
	// They will operate while we wait for Ready
	v.startSubRunners(lifetimeCtx)

	// Wait for RV to become ready
	if err := v.waitForReady(lifetimeCtx); err != nil {
		v.log.Error("failed waiting for RV to become ready", err)
		// Continue to cleanup
	}

	// Start checker after Ready (to monitor for state changes)
	v.startChecker(lifetimeCtx)

	// Wait for lifetime to expire or context to be cancelled
	<-lifetimeCtx.Done()

	// Cleanup sequence
	v.cleanup(ctx)

	return nil
}

func (v *VolumeMain) selectInitialPublishCount() int {
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

func (v *VolumeMain) selectInitialNodes(ctx context.Context, count int) ([]string, error) {
	if count == 0 {
		return nil, nil
	}

	nodes, err := v.client.SelectRandomNodes(ctx, count)
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
	params := logging.ActionParams{
		"storage_class":   v.storageClass,
		"initial_size":    v.initialSize.String(),
		"publish_nodes":   publishNodes,
		"lifetime_period": v.lifetimePeriod.String(),
		"api_version":     "v1alpha3",
	}

	v.log.ActionStarted("create_rv", params)
	startTime := time.Now()

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
			ReplicatedStorageClassName: v.storageClass, // Uses RSC for LVM config
			PublishOn:                  publishOn,
		},
	}

	err := v.client.CreateRV(ctx, rv)
	if err != nil {
		v.log.ActionFailed("create_rv", params, err, time.Since(startTime))
		return fmt.Errorf("creating RV: %w", err)
	}

	v.log.ActionCompleted("create_rv", params, "created", time.Since(startTime))
	return nil
}

func (v *VolumeMain) waitForReady(ctx context.Context) error {
	params := logging.ActionParams{}

	v.log.ActionStarted("wait_for_ready", params)
	startTime := time.Now()

	err := v.client.WaitForRVReady(ctx, v.rvName, rvCreateTimeout)
	if err != nil {
		v.log.ActionFailed("wait_for_ready", params, err, time.Since(startTime))
		return err
	}

	v.log.ActionCompleted("wait_for_ready", params, "ready", time.Since(startTime))
	return nil
}

func (v *VolumeMain) startSubRunners(ctx context.Context) {
	v.subRunnersMu.Lock()
	defer v.subRunnersMu.Unlock()

	// Create publisher configs
	publisher1Cfg := config.DefaultVolumePublisherConfig(30*time.Second, 60*time.Second)
	publisher2Cfg := config.DefaultVolumePublisherConfig(100*time.Second, 200*time.Second)

	// Create runners
	v.publishers = []*VolumePublisher{
		NewVolumePublisher(v.rvName, publisher1Cfg, v.client),
		NewVolumePublisher(v.rvName, publisher2Cfg, v.client),
	}

	// Start all runners
	for _, pub := range v.publishers {
		subCtx, cancel := context.WithCancel(ctx)
		v.subRunnersCancel = append(v.subRunnersCancel, cancel)
		go func(p *VolumePublisher) {
			if err := p.Run(subCtx); err != nil {
				v.log.Error("publisher error", err)
			}
		}(pub)
	}

	// Start resizer (if enabled)
	if !v.disableVolumeResizer {
		resizerCfg := config.DefaultVolumeResizerConfig()
		v.resizer = NewVolumeResizer(v.rvName, resizerCfg, v.client)
		resizerCtx, resizerCancel := context.WithCancel(ctx)
		v.subRunnersCancel = append(v.subRunnersCancel, resizerCancel)
		go func() {
			if err := v.resizer.Run(resizerCtx); err != nil {
				v.log.Error("resizer error", err)
			}
		}()
	} else {
		v.log.Info("volume-resizer goroutine is disabled")
	}

	// Start replica destroyer (if enabled)
	if !v.disableVolumeReplicaDestroyer {
		replicaDestroyerCfg := config.DefaultVolumeReplicaDestroyerConfig()
		v.replicaDestroyer = NewVolumeReplicaDestroyer(v.rvName, replicaDestroyerCfg, v.client)
		destroyerCtx, destroyerCancel := context.WithCancel(ctx)
		v.subRunnersCancel = append(v.subRunnersCancel, destroyerCancel)
		go func() {
			if err := v.replicaDestroyer.Run(destroyerCtx); err != nil {
				v.log.Error("replica destroyer error", err)
			}
		}()
	} else {
		v.log.Info("volume-replica-destroyer goroutine is disabled")
	}

	// Start replica creator (if enabled)
	if !v.disableVolumeReplicaCreator {
		replicaCreatorCfg := config.DefaultVolumeReplicaCreatorConfig()
		v.replicaCreator = NewVolumeReplicaCreator(v.rvName, replicaCreatorCfg, v.client)
		creatorCtx, creatorCancel := context.WithCancel(ctx)
		v.subRunnersCancel = append(v.subRunnersCancel, creatorCancel)
		go func() {
			if err := v.replicaCreator.Run(creatorCtx); err != nil {
				v.log.Error("replica creator error", err)
			}
		}()
	} else {
		v.log.Info("volume-replica-creator goroutine is disabled")
	}
}

func (v *VolumeMain) startChecker(ctx context.Context) {
	v.subRunnersMu.Lock()
	defer v.subRunnersMu.Unlock()

	v.checker = NewVolumeChecker(v.rvName, v.client)
	checkerCtx, checkerCancel := context.WithCancel(ctx)
	v.subRunnersCancel = append(v.subRunnersCancel, checkerCancel)
	go func() {
		if err := v.checker.Run(checkerCtx); err != nil {
			v.log.Error("checker error", err)
		}
	}()
}

func (v *VolumeMain) cleanup(ctx context.Context) {
	v.subRunnersMu.Lock()
	cancelFuncs := v.subRunnersCancel
	v.subRunnersCancel = nil
	v.subRunnersMu.Unlock()

	// Stop all sub-runners
	for _, cancel := range cancelFuncs {
		cancel()
	}

	// Give sub-runners time to gracefully stop (especially publishers doing unpublish)
	time.Sleep(2 * time.Second)

	// Delete RV
	v.deleteRV(ctx)
}

func (v *VolumeMain) deleteRV(ctx context.Context) {
	params := logging.ActionParams{}

	v.log.ActionStarted("delete_rv", params)
	startTime := time.Now()

	err := v.client.DeleteRV(ctx, v.rvName)
	if err != nil {
		v.log.ActionFailed("delete_rv", params, err, time.Since(startTime))
		return
	}

	// Wait for deletion
	err = v.client.WaitForRVDeleted(ctx, v.rvName, rvDeleteTimeout)
	if err != nil {
		v.log.ActionFailed("delete_rv", params, err, time.Since(startTime))
		return
	}

	v.log.ActionCompleted("delete_rv", params, "deleted", time.Since(startTime))
}
