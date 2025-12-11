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
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
	"github.com/google/uuid"
)

// MultiVolume orchestrates multiple volume-main instances and pod-destroyers
type MultiVolume struct {
	cfg    config.MultiVolumeConfig
	client *k8sclient.Client
	log    *logging.Logger

	// Tracking running volumes
	runningVolumes atomic.Int32
	volumesMu      sync.Mutex
	volumesCancels map[string]context.CancelFunc

	// Pod destroyers
	podDestroyersMu      sync.Mutex
	podDestroyersCancels []context.CancelFunc
}

// NewMultiVolume creates a new MultiVolume orchestrator
func NewMultiVolume(
	cfg config.MultiVolumeConfig,
	client *k8sclient.Client,
) *MultiVolume {
	return &MultiVolume{
		cfg:            cfg,
		client:         client,
		log:            logging.GlobalLogger("multivolume"),
		volumesCancels: make(map[string]context.CancelFunc),
	}
}

// Name returns the runner name
func (m *MultiVolume) Name() string {
	return "multivolume"
}

// Run starts the multivolume orchestration until context is cancelled
func (m *MultiVolume) Run(ctx context.Context) error {
	m.log.Info("starting multivolume orchestrator",
		"storage_classes", m.cfg.StorageClasses,
		"max_volumes", m.cfg.MaxVolumes,
	)
	defer m.log.Info("multivolume orchestrator finished")

	// Start pod destroyers (if enabled)
	if !m.cfg.DisablePodDestroyer {
		m.startPodDestroyers(ctx)
	} else {
		m.log.Info("pod-destroyer goroutines are disabled")
	}

	// Main volume creation loop
	for {
		select {
		case <-ctx.Done():
			m.cleanup()
			return nil
		default:
		}

		// Check if we can create more volumes
		currentCount := int(m.runningVolumes.Load())
		if currentCount < m.cfg.MaxVolumes {
			// Determine how many to create
			toCreate := randomInt(m.cfg.VolumeStep.Min, m.cfg.VolumeStep.Max)

			for i := 0; i < toCreate; i++ {
				// Select random storage class
				//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
				storageClass := m.cfg.StorageClasses[rand.Intn(len(m.cfg.StorageClasses))]

				// Select random volume period
				volPeriod := randomDuration(m.cfg.VolumePeriod)

				// Generate unique name
				rvName := fmt.Sprintf("megatest-%s", uuid.New().String()[:8])

				// Start volume-main
				m.startVolumeMain(ctx, rvName, storageClass, volPeriod)
			}
		}

		// Wait before next iteration
		if err := waitRandomWithContext(ctx, m.cfg.StepPeriod); err != nil {
			m.cleanup()
			return nil
		}
	}
}

func (m *MultiVolume) startPodDestroyers(ctx context.Context) {
	m.podDestroyersMu.Lock()
	defer m.podDestroyersMu.Unlock()

	// Agent pod destroyer
	agentCfg := config.DefaultPodDestroyerConfig(
		"d8-sds-replicated-volume",
		"app=sds-replicated-volume-agent",
		1, 2,
		30*time.Second, 60*time.Second,
	)
	m.startPodDestroyer(ctx, agentCfg, "agent")

	// Controller pod destroyer
	controllerCfg := config.DefaultPodDestroyerConfig(
		"d8-sds-replicated-volume",
		"app=sds-replicated-volume-controller",
		1, 3,
		30*time.Second, 60*time.Second,
	)
	m.startPodDestroyer(ctx, controllerCfg, "controller")

	// API server pod destroyer (less frequent)
	apiCfg := config.DefaultPodDestroyerConfig(
		"d8-sds-replicated-volume",
		"app=linstor-controller",
		1, 3,
		120*time.Second, 240*time.Second,
	)
	m.startPodDestroyer(ctx, apiCfg, "api-server")
}

func (m *MultiVolume) startPodDestroyer(ctx context.Context, cfg config.PodDestroyerConfig, name string) {
	destroyer, err := NewPodDestroyer(cfg, m.client)
	if err != nil {
		m.log.Error("failed to create pod destroyer", err, "name", name)
		return
	}

	destroyerCtx, cancel := context.WithCancel(ctx)
	m.podDestroyersCancels = append(m.podDestroyersCancels, cancel)

	go func() {
		if err := destroyer.Run(destroyerCtx); err != nil {
			m.log.Error("pod destroyer error", err, "name", name)
		}
	}()

	m.log.Info("started pod destroyer", "name", name, "namespace", cfg.Namespace, "selector", cfg.LabelSelector)
}

func (m *MultiVolume) startVolumeMain(ctx context.Context, rvName, storageClass string, lifetime time.Duration) {
	m.volumesMu.Lock()
	defer m.volumesMu.Unlock()

	cfg := config.VolumeMainConfig{
		StorageClassName:              storageClass,
		LifetimePeriod:                lifetime,
		InitialSize:                   resource.MustParse("100Mi"),
		DisableVolumeResizer:          m.cfg.DisableVolumeResizer,
		DisableVolumeReplicaDestroyer: m.cfg.DisableVolumeReplicaDestroyer,
		DisableVolumeReplicaCreator:   m.cfg.DisableVolumeReplicaCreator,
	}

	volumeMain := NewVolumeMain(rvName, cfg, m.client)

	volumeCtx, cancel := context.WithCancel(ctx)
	m.volumesCancels[rvName] = cancel
	m.runningVolumes.Add(1)

	go func() {
		defer func() {
			m.volumesMu.Lock()
			delete(m.volumesCancels, rvName)
			m.volumesMu.Unlock()
			m.runningVolumes.Add(-1)
		}()

		if err := volumeMain.Run(volumeCtx); err != nil {
			m.log.Error("volume-main error", err, "rv_name", rvName)
		}
	}()

	m.log.Info("started volume-main",
		"rv_name", rvName,
		"storage_class", storageClass,
		"lifetime", lifetime.String(),
	)
}

func (m *MultiVolume) cleanup() {
	m.log.Info("cleaning up multivolume orchestrator")

	// Stop pod destroyers
	m.podDestroyersMu.Lock()
	for _, cancel := range m.podDestroyersCancels {
		cancel()
	}
	m.podDestroyersCancels = nil
	m.podDestroyersMu.Unlock()

	// Stop all volume-mains
	m.volumesMu.Lock()
	for rvName, cancel := range m.volumesCancels {
		m.log.Info("stopping volume-main", "rv_name", rvName)
		cancel()
	}
	m.volumesMu.Unlock()

	// Wait for all volumes to finish
	for m.runningVolumes.Load() > 0 {
		m.log.Info("waiting for volumes to stop", "remaining", m.runningVolumes.Load())
		time.Sleep(2 * time.Second)
	}

	m.log.Info("cleanup complete")
}
