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
	StorageClasses []string
	Kubeconfig     string
	MaxVolumes     int
	StepMin        int
	StepMax        int
	StepPeriodMin  time.Duration
	StepPeriodMax  time.Duration
	VolPeriodMin   time.Duration
	VolPeriodMax   time.Duration
	LogLevel       string
}

func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		RunE: func(_ *cobra.Command, _ []string) error {
			if len(o.StorageClasses) == 0 {
				return errors.New("storage-classes flag is required")
			}

			if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
				return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
			}

			if o.StepMin < 1 {
				return errors.New("step-min must be at least 1")
			}

			if o.StepMax < o.StepMin {
				return errors.New("step-max must be greater than or equal to step-min")
			}

			if o.StepPeriodMin <= 0 {
				return errors.New("step-period-min must be positive")
			}

			if o.StepPeriodMax < o.StepPeriodMin {
				return errors.New("step-period-max must be greater than or equal to step-period-min")
			}

			if o.VolPeriodMin <= 0 {
				return errors.New("vol-period-min must be positive")
			}

			if o.VolPeriodMax < o.VolPeriodMin {
				return errors.New("vol-period-max must be greater than or equal to vol-period-min")
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
	rootCmd.Flags().IntVarP(&o.MaxVolumes, "max-volumes", "", 10, "Maximum number of concurrent volumes")
	rootCmd.Flags().IntVarP(&o.StepMin, "step-min", "", 1, "Minimum number of volumes to create per step")
	rootCmd.Flags().IntVarP(&o.StepMax, "step-max", "", 3, "Maximum number of volumes to create per step")
	rootCmd.Flags().DurationVarP(&o.StepPeriodMin, "step-period-min", "", 10*time.Second, "Minimum wait between creation steps")
	rootCmd.Flags().DurationVarP(&o.StepPeriodMax, "step-period-max", "", 30*time.Second, "Maximum wait between creation steps")
	rootCmd.Flags().DurationVarP(&o.VolPeriodMin, "vol-period-min", "", 60*time.Second, "Minimum volume lifetime")
	rootCmd.Flags().DurationVarP(&o.VolPeriodMax, "vol-period-max", "", 300*time.Second, "Maximum volume lifetime")
	rootCmd.Flags().StringVarP(&o.LogLevel, "log-level", "", "info", "Log level (allowed values: debug, info, warn, error)")

	if err := rootCmd.Execute(); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}
