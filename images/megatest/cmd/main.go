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

	// Convert log level string to slog.Level
	var logLevel slog.Level
	switch opt.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// Setup logger with stdout output
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: false,
	})
	log := slog.New(logHandler)
	slog.SetDefault(log)

	start := time.Now()
	log.Info("megatest started")

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Channel to broadcast second signal to all cleanup handlers
	// When closed, all readers will receive notification simultaneously (broadcast mechanism)
	forceCleanupChan := make(chan struct{})

	// Handle signals: first signal stops volume creation, second signal forces cleanup cancellation
	go func() {
		sig := <-sigChan
		log.Info("received first signal, stopping RV creation and cleanup", "signal", sig)
		cancel()

		// Wait for second signal to broadcast to all cleanup handlers
		sig = <-sigChan
		log.Info("received second signal, forcing cleanup cancellation for all", "signal", sig)
		close(forceCleanupChan) // Broadcast: all readers will get notification simultaneously
	}()

	// Create Kubernetes client
	kubeClient, err := kubeutils.NewClientWithKubeconfig(opt.Kubeconfig)
	if err != nil {
		log.Error("failed to create Kubernetes client", "error", err)
		os.Exit(1)
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

	// Create pod-destroyer configs
	podDestroyerAgentConfig := config.PodDestroyerConfig{
		Namespace:     opt.PodDestroyerAgentNamespace,
		LabelSelector: opt.PodDestroyerAgentLabelSelector,
		PodCount:      config.StepMinMax{Min: opt.PodDestroyerAgentPodCountMin, Max: opt.PodDestroyerAgentPodCountMax},
		Period:        config.DurationMinMax{Min: opt.PodDestroyerAgentPeriodMin, Max: opt.PodDestroyerAgentPeriodMax},
	}

	podDestroyerControllerConfig := config.PodDestroyerConfig{
		Namespace:     opt.PodDestroyerControllerNamespace,
		LabelSelector: opt.PodDestroyerControllerLabelSelector,
		PodCount:      config.StepMinMax{Min: opt.PodDestroyerControllerPodCountMin, Max: opt.PodDestroyerControllerPodCountMax},
		Period:        config.DurationMinMax{Min: opt.PodDestroyerControllerPeriodMin, Max: opt.PodDestroyerControllerPeriodMax},
	}

	multiVolume := runners.NewMultiVolume(cfg, podDestroyerAgentConfig, podDestroyerControllerConfig, kubeClient, forceCleanupChan)
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

	if logLevel >= slog.LevelDebug {
		fmt.Fprintf(os.Stdout, "Total time to create RV via API: %s (avg: %s)\n", stats.TotalCreateRVTime.String(), avgCreateTime.String())
	}
	fmt.Fprintf(os.Stdout, "Total create RV time: %s (avg: %s)\n", stats.TotalWaitForRVReadyTime.String(), avgWaitTime.String())
	fmt.Fprintf(os.Stdout, "Total delete RV time: %s (avg: %s)\n", stats.TotalDeleteRVTime.String(), avgDeleteTime.String())

	// Print checker statistics
	printCheckerStats(checkerStats)

	fmt.Fprintf(os.Stdout, "\nTest duration: %s\n", duration.String())

	os.Stdout.Sync()

	os.Exit(0)
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
		switch {
		case ioReady == 0 && quorum == 0:
			stableCount++ // No issues at all
		case ioReady%2 == 1 || quorum%2 == 1:
			brokenCount++ // Odd = still in bad state
		default:
			recoveredCount++ // Even >0 = had issues but recovered
		}
	}

	fmt.Fprintf(os.Stdout, "%s\n", "────────────────────────────────────────────────────────────────────────────────")
	fmt.Fprintf(os.Stdout, "Stable (0 transitions):        %d\n", stableCount)
	fmt.Fprintf(os.Stdout, "Recovered (even transitions):  %d\n", recoveredCount)
	fmt.Fprintf(os.Stdout, "Broken (odd transitions):      %d\n", brokenCount)
}
