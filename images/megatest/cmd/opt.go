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
	//LogLevel        string

	// Disable flags for goroutines
	DisablePodDestroyer           bool
	DisableVolumeResizer          bool
	DisableVolumeReplicaDestroyer bool
	DisableVolumeReplicaCreator   bool

	// PodDestroyer config for agent
	PodDestroyerAgentNamespace     string
	PodDestroyerAgentLabelSelector string
	PodDestroyerAgentPodCountMin   int
	PodDestroyerAgentPodCountMax   int
	PodDestroyerAgentPeriodMin     time.Duration
	PodDestroyerAgentPeriodMax     time.Duration

	// PodDestroyer config for controller
	PodDestroyerControllerNamespace     string
	PodDestroyerControllerLabelSelector string
	PodDestroyerControllerPodCountMin   int
	PodDestroyerControllerPodCountMax   int
	PodDestroyerControllerPeriodMin     time.Duration
	PodDestroyerControllerPeriodMax     time.Duration
}

func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		RunE: func(_ *cobra.Command, _ []string) error {
			if len(o.StorageClasses) == 0 {
				return errors.New("storage-classes flag is required")
			}

			//if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
			//	return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
			//}

			if o.VolumeStepMin < 1 {
				return errors.New("volume-step-min must be at least 1")
			}

			if o.VolumeStepMax < o.VolumeStepMin {
				return errors.New("volume-step-max must be greater than or equal to volume-step-min")
			}

			if o.StepPeriodMin <= 0 {
				return errors.New("step-period-min must be positive")
			}

			if o.StepPeriodMax < o.StepPeriodMin {
				return errors.New("step-period-max must be greater than or equal to step-period-min")
			}

			if o.VolumePeriodMin <= 0 {
				return errors.New("volume-period-min must be positive")
			}

			if o.VolumePeriodMax < o.VolumePeriodMin {
				return errors.New("volume-period-max must be greater than or equal to volume-period-min")
			}

			if o.MaxVolumes < 1 {
				return errors.New("max-volumes must be at least 1")
			}

			return nil
		},
	}

	// Exit after displaying the help information
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
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
	//rootCmd.Flags().StringVarP(&o.LogLevel, "log-level", "", "info", "Log level (allowed values: debug, info, warn, error)")

	// Disable flags for goroutines
	rootCmd.Flags().BoolVarP(&o.DisablePodDestroyer, "disable-pod-destroyer", "", false, "Disable pod-destroyer goroutines")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeResizer, "disable-volume-resizer", "", false, "Disable volume-resizer goroutine")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeReplicaDestroyer, "disable-volume-replica-destroyer", "", false, "Disable volume-replica-destroyer goroutine")
	rootCmd.Flags().BoolVarP(&o.DisableVolumeReplicaCreator, "disable-volume-replica-creator", "", false, "Disable volume-replica-creator goroutine")

	// PodDestroyer Agent flags
	rootCmd.Flags().StringVarP(&o.PodDestroyerAgentNamespace, "pod-destroyer-agent-namespace", "", "d8-sds-replicated-volume", "Namespace for agent pod-destroyer")
	rootCmd.Flags().StringVarP(&o.PodDestroyerAgentLabelSelector, "pod-destroyer-agent-label-selector", "", "app=sds-replicated-volume-controller-agent", "Label selector for agent pod-destroyer")
	rootCmd.Flags().IntVarP(&o.PodDestroyerAgentPodCountMin, "pod-destroyer-agent-pod-count-min", "", 1, "Minimum number of agent pods to delete")
	rootCmd.Flags().IntVarP(&o.PodDestroyerAgentPodCountMax, "pod-destroyer-agent-pod-count-max", "", 2, "Maximum number of agent pods to delete")
	rootCmd.Flags().DurationVarP(&o.PodDestroyerAgentPeriodMin, "pod-destroyer-agent-period-min", "", 30*time.Second, "Minimum period between agent pod deletions")
	rootCmd.Flags().DurationVarP(&o.PodDestroyerAgentPeriodMax, "pod-destroyer-agent-period-max", "", 60*time.Second, "Maximum period between agent pod deletions")

	// PodDestroyer Controller flags
	rootCmd.Flags().StringVarP(&o.PodDestroyerControllerNamespace, "pod-destroyer-controller-namespace", "", "d8-sds-replicated-volume", "Namespace for controller pod-destroyer")
	rootCmd.Flags().StringVarP(&o.PodDestroyerControllerLabelSelector, "pod-destroyer-controller-label-selector", "", "app=sds-replicated-volume-controller", "Label selector for controller pod-destroyer")
	rootCmd.Flags().IntVarP(&o.PodDestroyerControllerPodCountMin, "pod-destroyer-controller-pod-count-min", "", 1, "Minimum number of controller pods to delete")
	rootCmd.Flags().IntVarP(&o.PodDestroyerControllerPodCountMax, "pod-destroyer-controller-pod-count-max", "", 3, "Maximum number of controller pods to delete")
	rootCmd.Flags().DurationVarP(&o.PodDestroyerControllerPeriodMin, "pod-destroyer-controller-period-min", "", 30*time.Second, "Minimum period between controller pod deletions")
	rootCmd.Flags().DurationVarP(&o.PodDestroyerControllerPeriodMax, "pod-destroyer-controller-period-max", "", 60*time.Second, "Maximum period between controller pod deletions")

	if err := rootCmd.Execute(); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}
