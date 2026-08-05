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
	"time"

	"github.com/spf13/cobra"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/migrator"
)

// Opt holds the CLI options for the migrator.
type Opt struct {
	LogLevel            string
	Mode                string
	RetryInterval       time.Duration
	Stage2WorkerCount   int
	Stage4MaxIterations int
	Stage4PollInterval  time.Duration
}

// ModeCreateDirectoryForYourself is the --mode value that makes the migrator only
// create its host directory (config.MigratorHostDir) with the deckhouse owner and
// 0700 permissions, then exit. It is meant to run in an init container as root.
const ModeCreateDirectoryForYourself = "create-directory-for-yourself"

// Parse parses CLI flags using cobra.
func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		RunE: func(_ *cobra.Command, _ []string) error {
			if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
				return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
			}
			switch o.Mode {
			case "", ModeCreateDirectoryForYourself:
			default:
				return fmt.Errorf("invalid 'mode' (allowed values: %s)", ModeCreateDirectoryForYourself)
			}
			if o.Mode == "" {
				if o.RetryInterval <= 0 {
					return errors.New("retry-interval must be positive")
				}
				if o.Stage2WorkerCount < 1 {
					return errors.New("stage2-worker-count must be at least 1")
				}
				if o.Stage4MaxIterations < 1 {
					return errors.New("stage4-max-iterations must be at least 1")
				}
				if o.Stage4PollInterval <= 0 {
					return errors.New("stage4-poll-interval must be positive")
				}
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
	rootCmd.Flags().StringVar(&o.Mode, "mode", "", "Migrator mode. Empty means the normal migration flow. "+ModeCreateDirectoryForYourself+" only creates the migrator host directory (owned by the deckhouse user, mode 0700) and exits; it is meant to run as root in the Job init container.")
	rootCmd.Flags().DurationVar(&o.RetryInterval, "retry-interval", migrator.DefaultRetryInterval, "Interval between ConfigMap state update retries and stage 2 polling rounds")
	rootCmd.Flags().IntVar(&o.Stage2WorkerCount, "stage2-worker-count", migrator.DefaultStage2WorkerCount, "Number of parallel workers for stage 2")
	rootCmd.Flags().IntVar(&o.Stage4MaxIterations, "stage4-max-iterations", migrator.DefaultStage4MaxIterations, "Maximum number of stage 4 polling iterations while waiting for ReplicatedStorageClasses to become Ready")
	rootCmd.Flags().DurationVar(&o.Stage4PollInterval, "stage4-poll-interval", migrator.DefaultStage4PollInterval, "Interval between stage 4 polling iterations")
	if err := rootCmd.Execute(); err != nil {
		// Error is already logged by cobra.
		os.Exit(1)
	}
}
