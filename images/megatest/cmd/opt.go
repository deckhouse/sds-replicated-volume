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
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

	// Enable flags for goroutines
	EnablePodDestroyer           bool
	EnableVolumeResizer          bool
	EnableVolumeReplicaDestroyer bool
	EnableVolumeReplicaCreator   bool
	ChaosDebugMode               bool

	// Chaos engineering flags
	ParentKubeconfig        string
	VMNamespace             string
	EnableChaosNetBlock     bool
	EnableChaosNetDegrade   bool
	EnableChaosVMReboot     bool
	ChaosPeriodMin          time.Duration
	ChaosPeriodMax          time.Duration
	ChaosIncidentMin        time.Duration
	ChaosIncidentMax        time.Duration
	ChaosLossPercent        float64
	ChaosPartitionGroupSize int
}

// printGroupedFlags prints flags grouped by categories
func printGroupedFlags(cmd *cobra.Command) {
	// Define flag groups
	flagGroups := map[string][]string{
		"General": {
			"log-level",
			"chaos-debug-mode",
			"help",
		},
		"Basic goroutines": {
			"storage-classes",
			"kubeconfig",
			"max-volumes",
			"volume-step-min",
			"volume-step-max",
			"step-period-min",
			"step-period-max",
			"volume-period-min",
			"volume-period-max",
			"enable-pod-destroyer",
			"enable-volume-resizer",
			"enable-volume-replica-destroyer",
			"enable-volume-replica-creator",
		},
		"Chaos Engineering": {
			"parent-kubeconfig",
			"vm-namespace",
			"enable-chaos-network-block",
			"enable-chaos-network-degrade",
			"enable-chaos-vm-reboot",
			"chaos-period-min",
			"chaos-period-max",
			"chaos-incident-min",
			"chaos-incident-max",
			"chaos-loss-percent",
			"chaos-partition-group-size",
		},
	}

	// Group order
	groupOrder := []string{"General", "Basic goroutines", "Chaos Engineering"}

	// Collect all flags from command
	allFlags := make(map[string]*pflag.Flag)
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		allFlags[flag.Name] = flag
	})

	// Print Usage
	cmd.Println("Usage:")
	cmd.Printf("  %s [flags]\n\n", cmd.Use)

	// Collect remaining flags (not in any group)
	var remainingFlags []*pflag.Flag
	for _, flag := range allFlags {
		found := false
		for _, groupFlags := range flagGroups {
			for _, flagName := range groupFlags {
				if flag.Name == flagName {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			remainingFlags = append(remainingFlags, flag)
		}
	}

	// Print flag groups
	for _, groupName := range groupOrder {
		flagNames := flagGroups[groupName]
		var groupFlags []*pflag.Flag

		for _, flagName := range flagNames {
			if flag, exists := allFlags[flagName]; exists {
				groupFlags = append(groupFlags, flag)
			}
		}

		// For General group, also include remaining flags
		if groupName == "General" && len(remainingFlags) > 0 {
			groupFlags = append(groupFlags, remainingFlags...)
		}

		if len(groupFlags) > 0 {
			cmd.Printf("Flags (%s):\n", groupName)
			// Calculate max width for this group
			maxWidth := 0
			for _, flag := range groupFlags {
				width := calculateFlagWidth(flag)
				if width > maxWidth {
					maxWidth = width
				}
			}
			// Print all flags in the group with aligned descriptions
			for _, flag := range groupFlags {
				printFlag(cmd, flag, maxWidth)
			}
			cmd.Println()
		}
	}
}

// calculateFlagWidth calculates the display width of a flag including its type
func calculateFlagWidth(flag *pflag.Flag) int {
	width := 0
	if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
		// Format: "  -s, --flag-name"
		width = 6 + len(flag.Shorthand) + 4 + len(flag.Name)
	} else {
		// Format: "      --flag-name"
		width = 6 + len(flag.Name)
	}
	// Add type width if not bool
	if flag.Value.Type() != "bool" {
		// Format: " type"
		width += 1 + len(flag.Value.Type())
	}
	return width
}

// printFlag prints a single flag in cobra-style format with aligned description
func printFlag(cmd *cobra.Command, flag *pflag.Flag, maxWidth int) {
	var b strings.Builder

	if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
		fmt.Fprintf(&b, "  -%s, --%s", flag.Shorthand, flag.Name)
	} else {
		fmt.Fprintf(&b, "      --%s", flag.Name)
	}

	if flag.Value.Type() != "bool" {
		fmt.Fprintf(&b, " %s", flag.Value.Type())
	}

	// Add spacing to align descriptions
	flagLine := b.String()
	currentWidth := len(flagLine)
	if currentWidth < maxWidth {
		flagLine += strings.Repeat(" ", maxWidth-currentWidth+1)
	} else {
		flagLine += " "
	}

	cmd.Print(flagLine)
	cmd.Print(flag.Usage)

	// Add default value if present and not zero value
	defValue := flag.DefValue
	if defValue != "" && defValue != "false" && defValue != "0" && defValue != "[]" && defValue != `""` {
		cmd.Printf(" (default %s)", defValue)
	}
	cmd.Println()
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
			chaosEnabled := o.EnableChaosNetBlock ||
				o.EnableChaosNetDegrade || o.EnableChaosVMReboot

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
				if o.ChaosLossPercent < 0 || o.ChaosLossPercent > 1.0 {
					return errors.New("chaos-loss-percent must be between 0.0 and 1.0 (0.01 = 1%, 0.10 = 10%)")
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
		// Print grouped flags
		printGroupedFlags(cmd)
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

	// Enable flags for goroutines
	rootCmd.Flags().BoolVarP(&o.EnablePodDestroyer, "enable-pod-destroyer", "", false, "Enable pod-destroyer goroutines")
	rootCmd.Flags().BoolVarP(&o.EnableVolumeResizer, "enable-volume-resizer", "", false, "Enable volume-resizer goroutine")
	rootCmd.Flags().BoolVarP(&o.EnableVolumeReplicaDestroyer, "enable-volume-replica-destroyer", "", false, "Enable volume-replica-destroyer goroutine")
	rootCmd.Flags().BoolVarP(&o.EnableVolumeReplicaCreator, "enable-volume-replica-creator", "", false, "Enable volume-replica-creator goroutine")
	rootCmd.Flags().BoolVarP(&o.ChaosDebugMode, "chaos-debug-mode", "", false, "Enable chaos debug mode: disable basic runners and use stub mode for chaos goroutines debugging")

	// Chaos engineering flags
	rootCmd.Flags().StringVarP(&o.ParentKubeconfig, "parent-kubeconfig", "", "", "Path to kubeconfig for parent cluster (DVP)")
	rootCmd.Flags().StringVarP(&o.VMNamespace, "vm-namespace", "", "", "Namespace in parent cluster where VMs are located")

	// Chaos enable flags
	rootCmd.Flags().BoolVarP(&o.EnableChaosNetBlock, "enable-chaos-network-block", "", false, "Enable chaos-network-blocker goroutine")
	rootCmd.Flags().BoolVarP(&o.EnableChaosNetDegrade, "enable-chaos-network-degrade", "", false, "Enable chaos-network-degrader goroutine")
	rootCmd.Flags().BoolVarP(&o.EnableChaosVMReboot, "enable-chaos-vm-reboot", "", false, "Enable chaos-vm-reboter goroutine")

	// Chaos timing flags
	rootCmd.Flags().DurationVarP(&o.ChaosPeriodMin, "chaos-period-min", "", 60*time.Second, "Minimum wait between chaos events")
	rootCmd.Flags().DurationVarP(&o.ChaosPeriodMax, "chaos-period-max", "", 300*time.Second, "Maximum wait between chaos events")
	rootCmd.Flags().DurationVarP(&o.ChaosIncidentMin, "chaos-incident-min", "", 10*time.Second, "Minimum duration of chaos incident")
	rootCmd.Flags().DurationVarP(&o.ChaosIncidentMax, "chaos-incident-max", "", 60*time.Second, "Maximum duration of chaos incident")

	// Network degradation specific flags
	rootCmd.Flags().Float64VarP(&o.ChaosLossPercent, "chaos-loss-percent", "", 0.01, "Packet loss percentage; accepts value from 0.0 to 1.0 (0.01 = 1%, 0.10 = 10%)")

	// Network partition specific flags
	rootCmd.Flags().IntVarP(&o.ChaosPartitionGroupSize, "chaos-partition-group-size", "", 0, "Target size for partition group (0 = split in half, default: 0)")

	if err := rootCmd.Execute(); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}
