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

	"github.com/spf13/cobra"
)

type Opt struct {
	Mode      string
	Resources []string
	LogLevel  string
}

func (o *Opt) Parse() {
	var rootCmd = &cobra.Command{
		RunE: func(_ *cobra.Command, _ []string) error {
			if !regexp.MustCompile(`^get-resources$|^run-migrator$`).MatchString(o.Mode) {
				return errors.New("invalid 'mode'")
			}

			if !regexp.MustCompile(`^debug$|^info$|^warn$|^error$`).MatchString(o.LogLevel) {
				return errors.New("invalid 'log-level' (allowed values: debug, info, warn, error)")
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
	rootCmd.Flags().StringVarP(&o.Mode, "mode", "", "get-resources", "Launch mode (allowed values: get-resources, run-migrator)")
	rootCmd.Flags().StringSliceVarP(&o.Resources, "resources", "", nil, "Resources to migrate (comma-separated or repeated). If not used, all resources will be migrated. Example: pvc-af7df3dd-3d8f-4c1f-891c-af8433aa2633,pvc-af7df3dd-3d8f-4c1f-891c-af8433aa2644")
	rootCmd.Flags().StringVarP(&o.LogLevel, "log-level", "", "info", "Log level (allowed values: debug, info, warn, error)")
	if err := rootCmd.Execute(); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}
