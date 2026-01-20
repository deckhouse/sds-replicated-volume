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
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/spf13/cobra"
)

type Opt struct {
	StorageClasses  []string
	Kubeconfig      string
	MaxVolumes      int
	VolumeStepMin   int
	VolumeStepMax   int
	StepPeriodMin   time.Duration
	StepPeriodMax   time.Duration
	VolumePeriodMin time.Duration
	VolumePeriodMax time.Duration
	LogLevel        string

	// Disable flags for goroutines
	DisablePodDestroyer           bool
	DisableVolumeResizer          bool
	DisableVolumeReplicaDestroyer bool
	DisableVolumeReplicaCreator   bool

	// Chaos engineering flags
	ParentKubeconfig        string
	VMNamespace             string
	EnableChaosDRBDBlock    bool
	EnableChaosNetBlock     bool
	EnableChaosNetDegrade   bool
	EnableChaosVMReboot     bool
	EnableChaosNetPartition bool
	ChaosPeriodMin          time.Duration
	ChaosPeriodMax          time.Duration
	ChaosIncidentMin        time.Duration
	ChaosIncidentMax        time.Duration
	ChaosDelayMsMin         int
	ChaosDelayMsMax         int
	ChaosLossPercentMin     float64
	ChaosLossPercentMax     float64
	ChaosPartitionGroupSize int
}

func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		Use:   "megatest",
		Short: "A tool for testing ReplicatedVolume operations in Kubernetes",
		Long: `megatest is a testing tool that creates and manages multiple ReplicatedVolumes concurrently
to test the stability and performance of the SDS Replicated Volume system.

Graceful Shutdown:
  To stop megatest, press Ctrl+C (SIGINT). This will:
  1. Stop creating new ReplicatedVolumes
  2. Begin cleanup process that will delete all created ReplicatedVolumes and related resources
  3. After cleanup completes, display test statistics

Interrupting Cleanup:
  If you need to interrupt the cleanup process, press Ctrl+C a second time.
  This will force immediate termination, leaving all objects created by megatest
  in the cluster. These objects will need to be manually deleted later.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if len(o.StorageClasses) == 0 {
				return errors.New("storage-classes flag is required")
			}

			if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
				return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
			}

			// Range checks only (defaults are valid)
			if o.VolumeStepMax < o.VolumeStepMin {
				return errors.New("volume-step-max must be >= volume-step-min")
			}
			if o.StepPeriodMax < o.StepPeriodMin {
				return errors.New("step-period-max must be >= step-period-min")
			}
			if o.VolumePeriodMax < o.VolumePeriodMin {
				return errors.New("volume-period-max must be >= volume-period-min")
			}

			// Validate chaos flags if any chaos feature is enabled
			chaosEnabled := o.EnableChaosDRBDBlock || o.EnableChaosNetBlock ||
				o.EnableChaosNetDegrade || o.EnableChaosVMReboot || o.EnableChaosNetPartition

			if chaosEnabled {
				// Required flags (no defaults)
				if o.ParentKubeconfig == "" {
					return errors.New("parent-kubeconfig is required when chaos features are enabled")
				}
				if o.VMNamespace == "" {
					return errors.New("vm-namespace is required when chaos features are enabled")
				}
				// Range checks only
				if o.ChaosPeriodMax < o.ChaosPeriodMin {
					return errors.New("chaos-period-max must be >= chaos-period-min")
				}
				if o.ChaosIncidentMax < o.ChaosIncidentMin {
					return errors.New("chaos-incident-max must be >= chaos-incident-min")
				}
			}

			// Validate network degradation specific flags
			if o.EnableChaosNetDegrade {
				if o.ChaosDelayMsMax < o.ChaosDelayMsMin {
					return errors.New("chaos-delay-ms-max must be >= chaos-delay-ms-min")
				}
				if o.ChaosLossPercentMax < o.ChaosLossPercentMin {
					return errors.New("chaos-loss-percent-max must be >= chaos-loss-percent-min")
				}
				if o.ChaosLossPercentMin < 0 || o.ChaosLossPercentMax > 100 {
					return errors.New("chaos-loss-percent values must be between 0 and 100")
				}
			}

			return nil
		},
	}

	// Exit after displaying the help information
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		// Print Short description if available
		if cmd.Short != "" {
			cmd.Println(cmd.Short)
			cmd.Println()
		}
		// Print Long description if available
		if cmd.Long != "" {
			cmd.Println(cmd.Long)
			cmd.Println()
		}
		// Print usage and flags
		cmd.Print(cmd.UsageString())
		os.Exit(0)
	})

	// Add flags
	rootCmd.Flags().StringSliceVarP(&o.StorageClasses, "storage-classes", "", nil, "Comma-separated list of storage class names to use (required)")
	rootCmd.Flags().StringVarP(&o.Kubeconfig, "kubeconfig", "", "", "Path to kubeconfig file")
	rootCmd.Flags().IntVarP(&o.MaxVolumes, "max-volumes", "", 10, "Maximum number of concurrent ReplicatedVolumes")
	rootCmd.Flags().IntVarP(&o.VolumeStepMin, "volume-step-min", "", 1, "Minimum number of ReplicatedVolumes to create per step")
	rootCmd.Flags().IntVarP(&o.VolumeStepMax, "volume-step-max", "", 3, "Maximum number of ReplicatedVolumes to create per step")
	rootCmd.Flags().DurationVarP(&o.StepPeriodMin, "step-period-min", "", 10*time.Second, "Minimum wait between ReplicatedVolume creation steps")
	rootCmd.Flags().DurationVarP(&o.StepPeriodMax, "step-period-max", "", 30*time.Second, "Maximum wait between ReplicatedVolume creation steps")
	rootCmd.Flags().DurationVarP(&o.VolumePeriodMin, "volume-period-min", "", 60*time.Second, "Minimum ReplicatedVolume lifetime")
	rootCmd.Flags().DurationVarP(&o.VolumePeriodMax, "volume-period-max", "", 300*time.Second, "Maximum ReplicatedVolume lifetime")
	rootCmd.Flags().StringVarP(&o.LogLevel, "log-level", "", "info", "Log level (allowed values: debug, info, warn, error)")

	// Disable flags for goroutines
	rootCmd.Flags().BoolVarP(&o.DisablePodDestroyer, "disable-pod-destroyer", "", false, "Disable pod-destroyer goroutines")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeResizer, "disable-volume-resizer", "", false, "Disable volume-resizer goroutine")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeReplicaDestroyer, "disable-volume-replica-destroyer", "", false, "Disable volume-replica-destroyer goroutine")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeReplicaCreator, "disable-volume-replica-creator", "", false, "Disable volume-replica-creator goroutine")

	// Chaos engineering flags
	rootCmd.Flags().StringVarP(&o.ParentKubeconfig, "parent-kubeconfig", "", "", "Path to kubeconfig for parent cluster (DVP)")
	rootCmd.Flags().StringVarP(&o.VMNamespace, "vm-namespace", "", "", "Namespace in parent cluster where VMs are located")

	// Chaos enable flags
	rootCmd.Flags().BoolVarP(&o.EnableChaosDRBDBlock, "enable-chaos-drbd-block", "", false, "Enable DRBD port blocking chaos")
	rootCmd.Flags().BoolVarP(&o.EnableChaosNetBlock, "enable-chaos-network-block", "", false, "Enable full network blocking chaos")
	rootCmd.Flags().BoolVarP(&o.EnableChaosNetDegrade, "enable-chaos-network-degrade", "", false, "Enable network degradation chaos (latency/loss)")
	rootCmd.Flags().BoolVarP(&o.EnableChaosVMReboot, "enable-chaos-vm-reboot", "", false, "Enable VM hard reboot chaos")
	rootCmd.Flags().BoolVarP(&o.EnableChaosNetPartition, "enable-chaos-network-partition", "", false, "Enable network partition (split-brain) chaos")

	// Chaos timing flags
	rootCmd.Flags().DurationVarP(&o.ChaosPeriodMin, "chaos-period-min", "", 60*time.Second, "Minimum wait between chaos events")
	rootCmd.Flags().DurationVarP(&o.ChaosPeriodMax, "chaos-period-max", "", 300*time.Second, "Maximum wait between chaos events")
	rootCmd.Flags().DurationVarP(&o.ChaosIncidentMin, "chaos-incident-min", "", 10*time.Second, "Minimum duration of chaos incident")
	rootCmd.Flags().DurationVarP(&o.ChaosIncidentMax, "chaos-incident-max", "", 60*time.Second, "Maximum duration of chaos incident")

	// Network degradation specific flags
	rootCmd.Flags().IntVarP(&o.ChaosDelayMsMin, "chaos-delay-ms-min", "", 30, "Minimum network delay in milliseconds")
	rootCmd.Flags().IntVarP(&o.ChaosDelayMsMax, "chaos-delay-ms-max", "", 60, "Maximum network delay in milliseconds")
	rootCmd.Flags().Float64VarP(&o.ChaosLossPercentMin, "chaos-loss-percent-min", "", 1.0, "Minimum packet loss percentage")
	rootCmd.Flags().Float64VarP(&o.ChaosLossPercentMax, "chaos-loss-percent-max", "", 10.0, "Maximum packet loss percentage")

	// Network partition specific flags
	rootCmd.Flags().IntVarP(&o.ChaosPartitionGroupSize, "chaos-partition-group-size", "", 0, "Target size for partition group (0 = split in half)")

	if err := rootCmd.Execute(); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}
