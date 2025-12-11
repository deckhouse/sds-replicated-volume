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

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/runners"
	"github.com/google/uuid"
)

func main() {
	start := time.Now()

	// Parse options
	var opt Opt
	opt.Parse()

	// Setup logging
	var level slog.Level
	switch strings.ToLower(opt.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logging.SetupGlobalLogger(level)

	// Create Kubernetes client
	client, err := k8sclient.NewClientWithKubeconfig(opt.Kubeconfig)
	if err != nil {
		slog.Error("failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	// Create multivolume config
	cfg := config.MultiVolumeConfig{
		StorageClasses:                opt.StorageClasses,
		MaxVolumes:                    opt.MaxVolumes,
		VolumeStep:                    config.Count{Min: opt.VolumeStepMin, Max: opt.VolumeStepMax},
		StepPeriod:                    config.Duration{Min: opt.StepPeriodMin, Max: opt.StepPeriodMax},
		VolumePeriod:                  config.Duration{Min: opt.VolumePeriodMin, Max: opt.VolumePeriodMax},
		DisablePodDestroyer:           opt.DisablePodDestroyer,
		DisableVolumeResizer:          opt.DisableVolumeResizer,
		DisableVolumeReplicaDestroyer: opt.DisableVolumeReplicaDestroyer,
		DisableVolumeReplicaCreator:   opt.DisableVolumeReplicaCreator,
	}

	// Generate unique instance ID
	instanceID := uuid.New().String()[:8]

	// Create multivolume orchestrator
	multiVolume := runners.NewMultiVolume(cfg, client, instanceID)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Build list of disabled goroutines for logging
	var disabledGoroutines []string
	if opt.DisablePodDestroyer {
		disabledGoroutines = append(disabledGoroutines, "pod-destroyer")
	}
	if opt.DisableVolumeResizer {
		disabledGoroutines = append(disabledGoroutines, "volume-resizer")
	}
	if opt.DisableVolumeReplicaDestroyer {
		disabledGoroutines = append(disabledGoroutines, "volume-replica-destroyer")
	}
	if opt.DisableVolumeReplicaCreator {
		disabledGoroutines = append(disabledGoroutines, "volume-replica-creator")
	}

	// Run
	logParams := []any{
		"instance_id", instanceID,
		"storage_classes", opt.StorageClasses,
		"max_volumes", opt.MaxVolumes,
	}
	if len(disabledGoroutines) > 0 {
		logParams = append(logParams, "disabled_goroutines", disabledGoroutines)
	}
	slog.Info("megatest started", logParams...)

	if err := multiVolume.Run(ctx); err != nil {
		slog.Error("megatest failed", "error", err)
		os.Exit(1)
	}

	slog.Info("megatest finished", "duration", time.Since(start).String())
}
