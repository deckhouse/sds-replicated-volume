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
	"sync/atomic"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
)

// MultiVolume orchestrates multiple volume-main instances and pod-destroyers
type MultiVolume struct {
	cfg    config.MultiVolumeConfig
	client *kubeutils.Client
	log    *slog.Logger

	// Tracking running volumes
	runningVolumes atomic.Int32
}

// NewMultiVolume creates a new MultiVolume orchestrator
func NewMultiVolume(
	cfg config.MultiVolumeConfig,
	client *kubeutils.Client,
) *MultiVolume {
	return &MultiVolume{
		cfg:    cfg,
		client: client,
		log:    slog.Default().With("runner", "multivolume"),
	}
}

// Run starts the multivolume orchestration until context is cancelled
func (m *MultiVolume) Run(ctx context.Context) error {
	var disabledRunners []string
	if m.cfg.DisablePodDestroyer {
		disabledRunners = append(disabledRunners, "pod-destroyer")
	}
	if m.cfg.DisableVolumeResizer {
		disabledRunners = append(disabledRunners, "volume-resizer")
	}
	if m.cfg.DisableVolumeReplicaDestroyer {
		disabledRunners = append(disabledRunners, "volume-replica-destroyer")
	}
	if m.cfg.DisableVolumeReplicaCreator {
		disabledRunners = append(disabledRunners, "volume-replica-creator")
	}

	m.log.Info("started", "disabled_runners", disabledRunners)
	defer m.log.Info("finished")

	if m.cfg.DisablePodDestroyer {
		m.log.Debug("pod-destroyer runner are disabled")
	} else {
		//m.startPodDestroyers(ctx)
		m.log.Info("pod-destroyer runner are enabled")
	}

	// Main volume creation loop
	for {
		select {
		case <-ctx.Done():
			m.cleanup(ctx.Err())
			return nil
		default:
		}

		// Check if we can create more volumes
		currentVolumes := int(m.runningVolumes.Load())
		if currentVolumes < m.cfg.MaxVolumes {
			// Determine how many to create
			toCreate := randomInt(m.cfg.VolumeStep.Min, m.cfg.VolumeStep.Max)
			m.log.Debug("create volumes", "count", toCreate)

			for i := 0; i < toCreate; i++ {
				// Select random storage class
				//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
				storageClass := m.cfg.StorageClasses[rand.Intn(len(m.cfg.StorageClasses))]

				// Select random volume period
				volumeLifetime := randomDuration(m.cfg.VolumePeriod)

				// Generate unique name
				rvName := fmt.Sprintf("mgt-%s", uuid.New().String())

				// Start volume-main
				m.startVolumeMain(ctx, rvName, storageClass, volumeLifetime)
			}
		}

		// Wait before next iteration
		randomDuration := randomDuration(m.cfg.StepPeriod)
		m.log.Debug("wait before next iteration of volume creation", "duration", randomDuration.String())
		if err := waitWithContext(ctx, randomDuration); err != nil {
			m.cleanup(err)
			return nil
		}
	}
}

func (m *MultiVolume) cleanup(reason error) {
	log := m.log.With("reason", reason, "func", "cleanup")
	log.Info("started")
	defer log.Info("finished")

	for m.runningVolumes.Load() > 0 {
		log.Info("waiting for volumes to stop", "remaining", m.runningVolumes.Load())
		time.Sleep(1 * time.Second)
	}
}

func (m *MultiVolume) startVolumeMain(ctx context.Context, rvName string, storageClass string, volumeLifetime time.Duration) {
	cfg := config.VolumeMainConfig{
		StorageClassName:              storageClass,
		VolumeLifetime:                volumeLifetime,
		InitialSize:                   resource.MustParse("100Mi"),
		DisableVolumeResizer:          m.cfg.DisableVolumeResizer,
		DisableVolumeReplicaDestroyer: m.cfg.DisableVolumeReplicaDestroyer,
		DisableVolumeReplicaCreator:   m.cfg.DisableVolumeReplicaCreator,
	}
	volumeMain := NewVolumeMain(rvName, cfg, m.client)

	volumeCtx, cancel := context.WithCancel(ctx)
	m.runningVolumes.Add(1)

	go func() {
		defer func() {
			cancel()
			m.runningVolumes.Add(-1)
		}()

		_ = volumeMain.Run(volumeCtx)
	}()
}
