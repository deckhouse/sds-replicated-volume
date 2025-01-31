package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	sv1 "k8s.io/api/storage/v1"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
)

var resourcesSchemeFuncs = []func(*runtime.Scheme) error{
	srv.AddToScheme,
	snc.AddToScheme,
	v1.AddToScheme,
	sv1.AddToScheme,
}

var cfgFilePath string

var rootCmd = &cobra.Command{
	Use:     "sds-replicated-volume-scheduler",
	Version: "development",
	Short:   "a scheduler-extender for sds-replicated-volume",
	Long: `A scheduler-extender for sds-replicated-volume.
The extender implements filter and prioritize verbs.
The filter verb is "filter" and served at "/filter" via HTTP.
It filters out nodes that have less storage capacity than requested.
The prioritize verb is "prioritize" and served at "/prioritize" via HTTP.
It scores nodes with this formula:
    min(10, max(0, log2(capacity >> 30 / divisor)))
The default divisor is 1.  It can be changed with a command-line option.
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// to avoid printing usage information when error is returned
		cmd.SilenceUsage = true
		// to avoid printing errors (we log it closer to the place where it has happened)
		cmd.SilenceErrors = true
		return subMain(cmd.Context())
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFilePath, "config", "", "config file")
}

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}

func subMain(ctx context.Context) error {
	return nil
}
