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
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

var (
	publisherPeriodMinMax        = []int{30, 60}
	replicaDestroyerPeriodMinMax = []int{30, 300}
	replicaCreatorPeriodMinMax   = []int{30, 300}

	volumeResizerPeriodMinMax = []int{50, 50}
	volumeResizerStepMinMax   = []string{"51Mi", "101Mi"}
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
	checkerStarted    atomic.Bool

	// Statistics
	createdRVCount          *atomic.Int64
	totalCreateRVTime       *atomic.Int64 // nanoseconds
	totalDeleteRVTime       *atomic.Int64 // nanoseconds
	totalWaitForRVReadyTime *atomic.Int64 // nanoseconds

	// Callback to register checker stats in MultiVolume
	registerCheckerStats func(*CheckerStats)
}

// NewVolumeMain creates a new VolumeMain
func NewVolumeMain(
	rvName string,
	cfg config.VolumeMainConfig,
	client *kubeutils.Client,
	createdRVCount *atomic.Int64,
	totalCreateRVTime *atomic.Int64,
	totalDeleteRVTime *atomic.Int64,
	totalWaitForRVReadyTime *atomic.Int64,
	registerCheckerStats func(*CheckerStats),
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
		createdRVCount:                createdRVCount,
		totalCreateRVTime:             totalCreateRVTime,
		totalDeleteRVTime:             totalDeleteRVTime,
		totalWaitForRVReadyTime:       totalWaitForRVReadyTime,
		registerCheckerStats:          registerCheckerStats,
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
	createDuration, err := v.createRV(ctx, publishNodes)
	if err != nil {
		v.log.Error("failed to create RV", "error", err)
		return err
	}
	if v.totalCreateRVTime != nil {
		v.totalCreateRVTime.Add(createDuration.Nanoseconds())
	}

	// Start all sub-runners immediately after RV creation
	// They will operate while we wait for Ready
	v.startSubRunners(lifetimeCtx)

	// Wait for RV to become ready
	waitDuration, err := v.waitForRVReady(lifetimeCtx)
	if err != nil {
		v.log.Error("failed waiting for RV to become ready", "error", err)
		// Continue to cleanup
		// TODO: run volume-checker before cleanup
	} else {
		// Start checker after Ready (to monitor for state changes)
		v.log.Debug("RV is ready, starting checker")
		v.startVolumeChecker(lifetimeCtx)
	}
	if v.totalWaitForRVReadyTime != nil {
		v.totalWaitForRVReadyTime.Add(waitDuration.Nanoseconds())
	}

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

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cleanupCancel()

	// Cleanup can be interrupted by signal if the parent context was interrupted.
	// This is done so that RV deletion can be skipped for subsequent debugging
	if ctx.Err() != nil {
		log.Info("cleanup interrupted by parent context")
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			sig := <-sigChan
			log.Info("received signal, shutting down", "signal", sig)
			cleanupCancel()
		}()
	}

	//  Wait for ALL sub-runners to stop (including VolumeChecker)
waitLoop:
	for v.runningSubRunners.Load() > 0 {
		select {
		case <-cleanupCtx.Done():
			log.Info("cleanup interrupted, skipping sub-runners wait", "remaining", v.runningSubRunners.Load())
			break waitLoop
		default:
		}
		log.Debug("waiting for sub-runners to stop", "remaining", v.runningSubRunners.Load())
		time.Sleep(500 * time.Millisecond)
	}

	// Start volume-checker if it wasn't started earlier to capture final RV state before deletion
	if !v.checkerStarted.Load() {
		log.Debug("checker was not started earlier, starting it now to capture final state")
		v.startVolumeCheckerForFinalState(cleanupCtx, log)
	}

	deleteDuration, err := v.deleteRVAndWait(cleanupCtx, log)
	if err != nil {
		v.log.Error("failed to delete RV", "error", err)
	}
	if v.totalDeleteRVTime != nil {
		v.totalDeleteRVTime.Add(deleteDuration.Nanoseconds())
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

func (v *VolumeMain) createRV(ctx context.Context, publishNodes []string) (time.Duration, error) {
	startTime := time.Now()

	// Ensure PublishOn is never nil (use empty slice instead)
	publishOn := publishNodes
	if publishOn == nil {
		publishOn = []string{}
	}

	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.rvName,
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       v.initialSize,
			ReplicatedStorageClassName: v.storageClass,
			PublishOn:                  publishOn,
		},
	}

	err := v.client.CreateRV(ctx, rv)
	if err != nil {
		return time.Since(startTime), err
	}

	// Increment statistics counter on successful creation
	if v.createdRVCount != nil {
		v.createdRVCount.Add(1)
	}

	return time.Since(startTime), nil
}

func (v *VolumeMain) deleteRVAndWait(ctx context.Context, log *slog.Logger) (time.Duration, error) {
	startTime := time.Now()

	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: v.rvName,
		},
	}

	err := v.client.DeleteRV(ctx, rv)
	if err != nil {
		return time.Since(startTime), err
	}

	err = v.WaitForRVDeleted(ctx, log)
	if err != nil {
		return time.Since(startTime), err
	}

	return time.Since(startTime), nil
}

func (v *VolumeMain) waitForRVReady(ctx context.Context) (time.Duration, error) {
	startTime := time.Now()

	for {
		v.log.Debug("waiting for RV to become ready")

		select {
		case <-ctx.Done():
			return time.Since(startTime), ctx.Err()
		default:
		}

		rv, err := v.client.GetRV(ctx, v.rvName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return time.Since(startTime), err
		}

		if v.client.IsRVReady(rv) {
			return time.Since(startTime), nil
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumeMain) WaitForRVDeleted(ctx context.Context, log *slog.Logger) error {
	for {
		log.Debug("waiting for RV to be deleted")

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := v.client.GetRV(ctx, v.rvName)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
	}
}

func (v *VolumeMain) startSubRunners(ctx context.Context) {
	// Start publisher
	publisherCfg := config.VolumePublisherConfig{
		Period: config.DurationMinMax{
			Min: time.Duration(publisherPeriodMinMax[0]) * time.Second,
			Max: time.Duration(publisherPeriodMinMax[1]) * time.Second,
		},
	}
	publisher := NewVolumePublisher(v.rvName, publisherCfg, v.client, publisherPeriodMinMax)
	publisherCtx, cancel := context.WithCancel(ctx)
	go func() {
		v.runningSubRunners.Add(1)
		defer func() {
			cancel()
			v.runningSubRunners.Add(-1)
		}()

		_ = publisher.Run(publisherCtx)
	}()

	// Start replica destroyer
	if v.disableVolumeReplicaDestroyer {
		v.log.Debug("volume-replica-destroyer runner is disabled")
	} else {
		v.log.Debug("volume-replica-destroyer runner is enabled")
		replicaDestroyerCfg := config.VolumeReplicaDestroyerConfig{
			Period: config.DurationMinMax{
				Min: time.Duration(replicaDestroyerPeriodMinMax[0]) * time.Second,
				Max: time.Duration(replicaDestroyerPeriodMinMax[1]) * time.Second,
			},
		}
		replicaDestroyer := NewVolumeReplicaDestroyer(v.rvName, replicaDestroyerCfg, v.client, replicaDestroyerPeriodMinMax)
		destroyerCtx, cancel := context.WithCancel(ctx)
		go func() {
			v.runningSubRunners.Add(1)
			defer func() {
				cancel()
				v.runningSubRunners.Add(-1)
			}()

			_ = replicaDestroyer.Run(destroyerCtx)
		}()
	}

	// Start replica creator
	if v.disableVolumeReplicaCreator {
		v.log.Debug("volume-replica-creator runner is disabled")
	} else {
		v.log.Debug("volume-replica-creator runner is enabled")
		replicaCreatorCfg := config.VolumeReplicaCreatorConfig{
			Period: config.DurationMinMax{
				Min: time.Duration(replicaCreatorPeriodMinMax[0]) * time.Second,
				Max: time.Duration(replicaCreatorPeriodMinMax[1]) * time.Second,
			},
		}
		replicaCreator := NewVolumeReplicaCreator(v.rvName, replicaCreatorCfg, v.client, replicaCreatorPeriodMinMax)
		creatorCtx, cancel := context.WithCancel(ctx)
		go func() {
			v.runningSubRunners.Add(1)
			defer func() {
				cancel()
				v.runningSubRunners.Add(-1)
			}()

			_ = replicaCreator.Run(creatorCtx)
		}()
	}

	// Start resizer
	if v.disableVolumeResizer {
		v.log.Debug("volume-resizer runner is disabled")
	} else {
		v.log.Debug("volume-resizer runner is enabled")
		volumeResizerCfg := config.VolumeResizerConfig{
			Period: config.DurationMinMax{
				Min: time.Duration(volumeResizerPeriodMinMax[0]) * time.Second,
				Max: time.Duration(volumeResizerPeriodMinMax[1]) * time.Second,
			},
			Step: config.SizeMinMax{
				Min: resource.MustParse(volumeResizerStepMinMax[0]),
				Max: resource.MustParse(volumeResizerStepMinMax[1]),
			},
		}
		volumeResizer := NewVolumeResizer(v.rvName, volumeResizerCfg, v.client, volumeResizerPeriodMinMax, volumeResizerStepMinMax)
		resizerCtx, cancel := context.WithCancel(ctx)
		go func() {
			v.runningSubRunners.Add(1)
			defer func() {
				cancel()
				v.runningSubRunners.Add(-1)
			}()

			_ = volumeResizer.Run(resizerCtx)
		}()
	}
}

func (v *VolumeMain) startVolumeChecker(ctx context.Context) {
	// Mark checker as started
	v.checkerStarted.Store(true)

	// Create stats for this checker and register in MultiVolume
	stats := &CheckerStats{RVName: v.rvName}
	if v.registerCheckerStats != nil {
		v.registerCheckerStats(stats)
	}

	volumeChecker := NewVolumeChecker(v.rvName, v.client, stats)
	checkerCtx, cancel := context.WithCancel(ctx)
	go func() {
		v.runningSubRunners.Add(1)
		defer func() {
			cancel()
			v.runningSubRunners.Add(-1)
		}()

		_ = volumeChecker.Run(checkerCtx)
	}()
}

// startVolumeCheckerForFinalState starts a volume checker briefly to capture the final state
// of the RV before deletion. This is used when the checker wasn't started earlier (e.g., if RV
// never reached Ready state). The checker will capture the current state via checkInitialState
// and then exit.
func (v *VolumeMain) startVolumeCheckerForFinalState(ctx context.Context, log *slog.Logger) {
	// Create stats for this checker and register in MultiVolume
	stats := &CheckerStats{RVName: v.rvName}
	if v.registerCheckerStats != nil {
		v.registerCheckerStats(stats)
	}

	volumeChecker := NewVolumeChecker(v.rvName, v.client, stats)

	// Create a context with timeout to allow checker to capture state and exit
	// 5 seconds should be enough for checkInitialState to complete
	checkerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Run checker synchronously - it will capture initial state and exit when context is done
	// This ensures we capture the final state before deletion
	if err := volumeChecker.Run(checkerCtx); err != nil {
		log.Debug("checker finished with error (expected)", "error", err)
	} else {
		log.Debug("checker finished successfully")
	}
}
