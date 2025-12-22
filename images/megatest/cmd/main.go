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

	// to ensure statistics are always printed
	exitCode := 0
	defer func() {
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
	// Stopping Informers whom uses by VolumeChecker
	defer kubeClient.StopInformers()

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
	multiVolume := runners.NewMultiVolume(cfg, kubeClient)
	_ = multiVolume.Run(ctx)

	// Print statistics
	stats := multiVolume.GetStats()
	checkerStats := multiVolume.GetCheckerStats()
	duration := time.Since(start)

	fmt.Fprintf(os.Stdout, "\nStatistics:\n")
	fmt.Fprintf(os.Stdout, "Total ReplicatedVolumes created: %d\n", stats.CreatedRVCount)

	// Calculate average times
	var avgCreateTime, avgDeleteTime, avgWaitTime time.Duration
	if stats.CreatedRVCount > 0 {
		avgCreateTime = stats.TotalCreateRVTime / time.Duration(stats.CreatedRVCount)
		avgDeleteTime = stats.TotalDeleteRVTime / time.Duration(stats.CreatedRVCount)
		avgWaitTime = stats.TotalWaitForRVReadyTime / time.Duration(stats.CreatedRVCount)
	}

	fmt.Fprintf(os.Stdout, "Total create RV time: %s (avg: %s)\n", stats.TotalCreateRVTime.String(), avgCreateTime.String())
	fmt.Fprintf(os.Stdout, "Total delete RV time: %s (avg: %s)\n", stats.TotalDeleteRVTime.String(), avgDeleteTime.String())
	fmt.Fprintf(os.Stdout, "Total wait for RV ready time: %s (avg: %s)\n", stats.TotalWaitForRVReadyTime.String(), avgWaitTime.String())

	// Print checker statistics
	printCheckerStats(checkerStats)

	fmt.Fprintf(os.Stdout, "Test duration: %s\n", duration.String())

	os.Stdout.Sync()
}

// printCheckerStats prints a summary table of all checker statistics
func printCheckerStats(stats []*runners.CheckerStats) {
	if len(stats) == 0 {
		fmt.Fprintf(os.Stdout, "\nChecker Statistics: no data\n")
		return
	}

	fmt.Fprintf(os.Stdout, "\nChecker Statistics:\n")
	fmt.Fprintf(os.Stdout, "%-40s %20s %20s\n", "RV Name", "IOReady Transitions", "Quorum Transitions")
	fmt.Fprintf(os.Stdout, "%s\n", "────────────────────────────────────────────────────────────────────────────────")

	var stableCount, recoveredCount, brokenCount int

	for _, s := range stats {
		ioReady := s.IOReadyTransitions.Load()
		quorum := s.QuorumTransitions.Load()

		fmt.Fprintf(os.Stdout, "%-40s %20d %20d\n", s.RVName, ioReady, quorum)

		// Categorize RV state
		if ioReady == 0 && quorum == 0 {
			stableCount++ // No issues at all
		} else if ioReady%2 == 1 || quorum%2 == 1 {
			brokenCount++ // Odd = still in bad state
		} else {
			recoveredCount++ // Even >0 = had issues but recovered
		}
	}

	fmt.Fprintf(os.Stdout, "%s\n", "────────────────────────────────────────────────────────────────────────────────")
	fmt.Fprintf(os.Stdout, "Stable (0 transitions):        %d\n", stableCount)
	fmt.Fprintf(os.Stdout, "Recovered (even transitions):  %d\n", recoveredCount)
	fmt.Fprintf(os.Stdout, "Broken (odd transitions):      %d\n", brokenCount)
}
