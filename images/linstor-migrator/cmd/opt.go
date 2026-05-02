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

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/migrator"
)

// Opt holds the CLI options for the migrator.
type Opt struct {
	LogLevel           string
	Stage2PollInterval time.Duration
	Stage2WorkerCount  int
}

// Parse parses CLI flags using cobra.
func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		RunE: func(_ *cobra.Command, _ []string) error {
			if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
				return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
			}
			if o.Stage2PollInterval <= 0 {
				return errors.New("stage2-poll-interval must be positive")
			}
			if o.Stage2WorkerCount < 1 {
				return errors.New("stage2-worker-count must be at least 1")
			}

			return nil
		},
	}

	// Exit after displaying the help information.
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		cmd.Print(cmd.UsageString())
		os.Exit(0)
	})

	rootCmd.Flags().StringVarP(&o.LogLevel, "log-level", "", "info", "Log level (allowed values: debug, info, warn, error)")
	rootCmd.Flags().DurationVar(&o.Stage2PollInterval, "stage2-poll-interval", migrator.DefaultStage2PollInterval, "Interval between stage 2 polling rounds (adopt exit-maintenance)")
	rootCmd.Flags().IntVar(&o.Stage2WorkerCount, "stage2-worker-count", migrator.DefaultStage2WorkerCount, "Number of parallel workers for stage 2")
	if err := rootCmd.Execute(); err != nil {
		// Error is already logged by cobra.
		os.Exit(1)
	}
}
