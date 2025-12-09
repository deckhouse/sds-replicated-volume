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
	"flag"
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
	// Parse flags
	var (
		storageClasses string
		maxVolumes     int
		stepMin        int
		stepMax        int
		stepPeriodMin  time.Duration
		stepPeriodMax  time.Duration
		volPeriodMin   time.Duration
		volPeriodMax   time.Duration
		logLevel       string
	)

	flag.StringVar(&storageClasses, "storage-classes", "", "Comma-separated list of storage class names to use")
	flag.IntVar(&maxVolumes, "max-volumes", 10, "Maximum number of concurrent volumes")
	flag.IntVar(&stepMin, "step-min", 1, "Minimum number of volumes to create per step")
	flag.IntVar(&stepMax, "step-max", 3, "Maximum number of volumes to create per step")
	flag.DurationVar(&stepPeriodMin, "step-period-min", 10*time.Second, "Minimum wait between creation steps")
	flag.DurationVar(&stepPeriodMax, "step-period-max", 30*time.Second, "Maximum wait between creation steps")
	flag.DurationVar(&volPeriodMin, "vol-period-min", 60*time.Second, "Minimum volume lifetime")
	flag.DurationVar(&volPeriodMax, "vol-period-max", 300*time.Second, "Maximum volume lifetime")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	// Setup logging
	var level slog.Level
	switch strings.ToLower(logLevel) {
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

	// Parse storage classes
	if storageClasses == "" {
		slog.Error("storage-classes flag is required")
		os.Exit(1)
	}
	scList := strings.Split(storageClasses, ",")
	for i := range scList {
		scList[i] = strings.TrimSpace(scList[i])
	}

	// Create Kubernetes client
	client, err := k8sclient.NewClient()
	if err != nil {
		slog.Error("failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	// Create multivolume config
	cfg := config.MultiVolumeConfig{
		StorageClasses: scList,
		MaxVolumes:     maxVolumes,
		Step:           config.Count{Min: stepMin, Max: stepMax},
		StepPeriod:     config.Duration{Min: stepPeriodMin, Max: stepPeriodMax},
		VolumePeriod:   config.Duration{Min: volPeriodMin, Max: volPeriodMax},
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

	// Run
	slog.Info("starting megatest",
		"instance_id", instanceID,
		"storage_classes", scList,
		"max_volumes", maxVolumes,
	)

	if err := multiVolume.Run(ctx); err != nil {
		slog.Error("megatest failed", "error", err)
		os.Exit(1)
	}

	slog.Info("megatest completed")
}
