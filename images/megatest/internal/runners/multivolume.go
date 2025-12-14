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
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// MultiVolume orchestrates multiple volume-main instances and pod-destroyers
type MultiVolume struct {
	cfg    config.MultiVolumeConfig
	client *kubeutils.Client
	log    *slog.Logger
	rLog   *slog.Logger
}

// NewMultiVolume creates a new MultiVolume orchestrator
func NewMultiVolume(
	cfg config.MultiVolumeConfig,
	log *slog.Logger,
	client *kubeutils.Client,
) *MultiVolume {
	return &MultiVolume{
		cfg:    cfg,
		client: client,
		log:    log,
		rLog:   log.With("runner", "multivolume"),
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

	m.rLog.Info("multivolume started", "disabled_runners", disabledRunners)
	defer m.rLog.Info("multivolume finished")

	if m.cfg.DisablePodDestroyer {
		m.rLog.Debug("pod-destroyer runner are disabled")
	} else {
		//m.startPodDestroyers(ctx)
		m.rLog.Info("pod-destroyer runner are enabled")
	}

	// Main volume creation loop
	for {
		select {
		case <-ctx.Done():
			m.rLog.Info("ctx done 1111")
			m.cleanup()
			return nil
		default:
		}

		m.startVolumeMain(ctx)

		select {
		case <-ctx.Done():
			m.rLog.Info("ctx done 2222")
			m.cleanup()
			return nil
		case <-time.After(30 * time.Second):
		}
	}
}

func (m *MultiVolume) cleanup() {
	m.rLog.Info("cleanup")
}

func (m *MultiVolume) startVolumeMain(ctx context.Context) {
	m.rLog.Info("startVolumeMain")
	_ = ctx
	for i := 0; i < 3; i++ {
		m.rLog.Debug(fmt.Sprintf("-- %d", i))
		time.Sleep(1 * time.Second)
	}
}
