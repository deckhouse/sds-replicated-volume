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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/runners"
)

func main() {
	// Parse options
	var opt Opt
	opt.Parse()

	// Setup logger with stdout output
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	log := slog.New(logHandler)
	slog.SetDefault(log)

	start := time.Now()
	log.Info("megatest started")

	exitCode := 0
	var multiVolume *runners.MultiVolume
	defer func() {
		if multiVolume != nil {
			stats := multiVolume.GetStats()
			duration := time.Since(start)

			fmt.Fprintf(os.Stderr, "\nStatistics:\n")
			fmt.Fprintf(os.Stderr, "Total ReplicatedVolumes created: %d\n", stats.CreatedRVCount)

			// Calculate average times
			var avgCreateTime, avgDeleteTime, avgWaitTime time.Duration
			if stats.CreatedRVCount > 0 {
				avgCreateTime = stats.TotalCreateRVTime / time.Duration(stats.CreatedRVCount)
				avgDeleteTime = stats.TotalDeleteRVTime / time.Duration(stats.CreatedRVCount)
				avgWaitTime = stats.TotalWaitForRVReadyTime / time.Duration(stats.CreatedRVCount)
			}

			fmt.Fprintf(os.Stderr, "Total create RV time: %s (avg: %s)\n", stats.TotalCreateRVTime.String(), avgCreateTime.String())
			fmt.Fprintf(os.Stderr, "Total delete RV time: %s (avg: %s)\n", stats.TotalDeleteRVTime.String(), avgDeleteTime.String())
			fmt.Fprintf(os.Stderr, "Total wait for RV ready time: %s (avg: %s)\n", stats.TotalWaitForRVReadyTime.String(), avgWaitTime.String())

			fmt.Fprintf(os.Stderr, "Test duration: %s\n", duration.String())

			os.Stderr.Sync()
		}

		os.Exit(exitCode)
	}()

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Create Kubernetes client
	kubeClient, err := kubeutils.NewClientWithKubeconfig(opt.Kubeconfig)
	if err != nil {
		log.Error("failed to create Kubernetes client", "error", err)
		exitCode = 1
		return
	}

	// Create multivolume config
	cfg := config.MultiVolumeConfig{
		StorageClasses:                opt.StorageClasses,
		MaxVolumes:                    opt.MaxVolumes,
		VolumeStep:                    config.StepMinMax{Min: opt.VolumeStepMin, Max: opt.VolumeStepMax},
		StepPeriod:                    config.DurationMinMax{Min: opt.StepPeriodMin, Max: opt.StepPeriodMax},
		VolumePeriod:                  config.DurationMinMax{Min: opt.VolumePeriodMin, Max: opt.VolumePeriodMax},
		DisablePodDestroyer:           opt.DisablePodDestroyer,
		DisableVolumeResizer:          opt.DisableVolumeResizer,
		DisableVolumeReplicaDestroyer: opt.DisableVolumeReplicaDestroyer,
		DisableVolumeReplicaCreator:   opt.DisableVolumeReplicaCreator,
	}
	multiVolume = runners.NewMultiVolume(cfg, kubeClient)
	if err := multiVolume.Run(ctx); err != nil {
		log.Error("failed to run multivolume", "error", err)
		exitCode = 1
	}
}
