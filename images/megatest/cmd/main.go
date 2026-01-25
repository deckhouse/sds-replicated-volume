/*
Copyright 2026 Flant JSC

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
	"sync"
	"syscall"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/chaos"
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

	// Create Kubernetes client first, before setting up signal handling
	// This allows us to exit early if cluster is unreachable
	kubeClient, err := kubeutils.NewClientWithKubeconfig(opt.Kubeconfig)
	if err != nil {
		log.Error("failed to create Kubernetes client", "error", err)
		os.Exit(1)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Stopping Informers whom uses by VolumeChecker
	defer kubeClient.StopInformers()

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

	// Create multivolume config
	cfg := config.MultiVolumeConfig{
		StorageClasses:               opt.StorageClasses,
		MaxVolumes:                   opt.MaxVolumes,
		VolumeStep:                   config.StepMinMax{Min: opt.VolumeStepMin, Max: opt.VolumeStepMax},
		StepPeriod:                   config.DurationMinMax{Min: opt.StepPeriodMin, Max: opt.StepPeriodMax},
		VolumePeriod:                 config.DurationMinMax{Min: opt.VolumePeriodMin, Max: opt.VolumePeriodMax},
		EnablePodDestroyer:           opt.EnablePodDestroyer,
		EnableVolumeResizer:          opt.EnableVolumeResizer,
		EnableVolumeReplicaDestroyer: opt.EnableVolumeReplicaDestroyer,
		EnableVolumeReplicaCreator:   opt.EnableVolumeReplicaCreator,
	}

	multiVolume := runners.NewMultiVolume(cfg, kubeClient, forceCleanupChan)
	_ = multiVolume.Run(ctx)

	// ----------- chaos ---------------------
	// Setup chaos engineering if enabled
	chaosEnabled := opt.EnableChaosNetBlock ||
		opt.EnableChaosNetDegrade || opt.EnableChaosVMReboot

	var chaosCleanup func()
	if chaosEnabled {
		chaosCleanup = setupChaosRunners(ctx, log, opt, kubeClient, forceCleanupChan)
		defer chaosCleanup()
	}
	// ----------- chaos ---------------------

	// Print statistics
	stats := multiVolume.GetStats()
	checkerStats := multiVolume.GetCheckerStats()
	duration := time.Since(start)

	fmt.Fprintf(os.Stdout, "\nStatistics:\n")
	fmt.Fprintf(os.Stdout, "Total RV created: %d\n", stats.CreatedRVCount)
	fmt.Fprintf(os.Stdout, "Total create RV errors: %d\n", stats.CreateRVErrorCount)

	// Calculate average times
	var avgCreateTime, avgDeleteTime, avgWaitTime time.Duration
	if stats.CreatedRVCount > 0 {
		avgCreateTime = stats.TotalCreateRVTime / time.Duration(stats.CreatedRVCount)
		avgDeleteTime = stats.TotalDeleteRVTime / time.Duration(stats.CreatedRVCount)
		avgWaitTime = stats.TotalWaitForRVReadyTime / time.Duration(stats.CreatedRVCount)
	}

	if logLevel >= slog.LevelDebug {
		fmt.Fprintf(os.Stdout, "Total time to create RV via API and RVAs: %s (avg: %s)\n", stats.TotalCreateRVTime.String(), avgCreateTime.String())
	}
	fmt.Fprintf(os.Stdout, "Total create RV time: %s (avg: %s)\n", stats.TotalWaitForRVReadyTime.String(), avgWaitTime.String())
	fmt.Fprintf(os.Stdout, "Total delete RV time: %s (avg: %s)\n", stats.TotalDeleteRVTime.String(), avgDeleteTime.String())

	// Print checker statistics
	printCheckerStats(checkerStats)

	fmt.Fprintf(os.Stdout, "\nTest duration: %s\n", duration.String())

	os.Stdout.Sync()

	// Function returns normally, defer statements will execute
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

// setupChaosRunners initializes and starts chaos engineering runners
// Returns a cleanup function that should be called on shutdown
func setupChaosRunners(
	ctx context.Context,
	log *slog.Logger,
	opt Opt,
	kubeClient *kubeutils.Client,
	forceCleanupChan <-chan struct{},
) func() {
	log.Info("setting up chaos engineering runners")

	// Create parent cluster client
	parentClient, err := chaos.NewParentClient(opt.ParentKubeconfig, opt.VMNamespace)
	if err != nil {
		log.Error("failed to create parent cluster client", "error", err)
		return func() {}
	}

	// Create Cilium policy manager (uses child cluster client)
	ciliumManager := chaos.NewCiliumPolicyManager(kubeClient.Client())

	// Create network degrade manager (uses child cluster client)
	networkDegradeMgr := chaos.NewNetworkDegradeManager(kubeClient.Client())

	// Cleanup stale resources from previous runs
	log.Info("cleaning up stale chaos resources from previous runs")
	if stalePolicies, err := ciliumManager.CleanupStaleChaosPolicies(ctx, opt.VMNamespace); err != nil {
		log.Warn("failed to cleanup stale Cilium policies", "error", err)
	} else if stalePolicies > 0 {
		log.Info("cleaned up stale Cilium policies", "count", stalePolicies)
	}

	if staleJobs, err := networkDegradeMgr.CleanupStaleNetworkDegradeJobs(ctx); err != nil {
		log.Warn("failed to cleanup stale network degrade Jobs", "error", err)
	} else if staleJobs > 0 {
		log.Info("cleaned up stale network degrade Jobs", "count", staleJobs)
	}

	if staleVMOps, err := parentClient.CleanupStaleVMOperations(ctx); err != nil {
		log.Warn("failed to cleanup stale VMOperations", "error", err)
	} else if staleVMOps > 0 {
		log.Info("cleaned up stale VMOperations", "count", staleVMOps)
	}

	// Common timing config
	period := config.DurationMinMax{Min: opt.ChaosPeriodMin, Max: opt.ChaosPeriodMax}
	incidentDuration := config.DurationMinMax{Min: opt.ChaosIncidentMin, Max: opt.ChaosIncidentMax}

	// Track running chaos goroutines
	var wg sync.WaitGroup

	// Start network blocker
	if opt.EnableChaosNetBlock {
		cfg := config.ChaosNetworkBlockerConfig{
			Period:           period,
			IncidentDuration: incidentDuration,
			GroupSize:        opt.ChaosPartitionGroupSize,
		}
		blocker := runners.NewChaosNetworkBlocker(cfg, ciliumManager, parentClient, forceCleanupChan)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = blocker.Run(ctx)
		}()
		log.Info("started chaos-network-blocker")
	}

	// Start network degrader
	if opt.EnableChaosNetDegrade {
		cfg := config.ChaosNetworkDegraderConfig{
			Period:           period,
			IncidentDuration: incidentDuration,
			LossPercent:      opt.ChaosLossPercent,
		}
		degrader := runners.NewChaosNetworkDegrader(cfg, networkDegradeMgr, parentClient, forceCleanupChan)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = degrader.Run(ctx)
		}()
		log.Info("started chaos-network-degrader")
	}

	// Start VM reboter
	if opt.EnableChaosVMReboot {
		cfg := config.ChaosVMReboterConfig{
			Period: period,
		}
		reboter := runners.NewChaosVMReboter(cfg, parentClient)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reboter.Run(ctx)
		}()
		log.Info("started chaos-vm-reboter")
	}

	// Return cleanup function
	return func() {
		log.Info("cleaning up chaos resources")

		// Wait for all chaos goroutines to finish
		wg.Wait()

		// Cleanup Cilium policies
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := ciliumManager.CleanupAllChaosPolicies(cleanupCtx, opt.VMNamespace); err != nil {
			log.Error("failed to cleanup Cilium policies", "error", err)
		}

		// Cleanup network degrade Jobs
		// Note: Jobs are self-cleaning via TTL, but we try to delete them explicitly
		// The cleanup is best-effort since Jobs may have already completed

		// Cleanup VM operations
		if err := parentClient.CleanupVMOperations(cleanupCtx); err != nil {
			log.Error("failed to cleanup VM operations", "error", err)
		}

		log.Info("chaos cleanup completed")
	}
}
