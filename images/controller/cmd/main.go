package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/deckhouse/sds-common-lib/slogh"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

	. "github.com/deckhouse/sds-common-lib/u"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	ctx := signals.SetupSignalHandler()

	logHandler := slogh.NewHandler(
		// TODO: fix slogh reload
		slogh.Config{
			Level:  slogh.LevelDebug,
			Format: slogh.FormatText,
		},
	)

	log := slog.New(logHandler).
		With("startedAt", time.Now().Format(time.RFC3339))

	crlog.SetLogger(logr.FromSlogHandler(logHandler))

	// TODO: fix slogh reload
	// slogh.RunConfigFileWatcher(
	// 	ctx,
	// 	func(data map[string]string) error {
	// 		err := logHandler.UpdateConfigData(data)
	// 		log.Info("UpdateConfigData", "data", data)
	// 		return err
	// 	},
	// 	&slogh.ConfigFileWatcherOptions{
	// 		OwnLogger: log.With("goroutine", "slogh"),
	// 	},
	// )

	log.Info("agent started")

	err := runAgent(ctx, log)
	if !errors.Is(err, context.Canceled) || ctx.Err() != context.Canceled {
		log.Error("agent exited unexpectedly", "err", err, "ctxerr", ctx.Err())
		os.Exit(1)
	}
	log.Info(
		"agent gracefully shutdown",
		// cleanup errors do not affect status code, but worth logging
		"err", err,
	)
}

func runAgent(ctx context.Context, log *slog.Logger) (err error) {
	// to be used in goroutines spawned below
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	envConfig, err := GetEnvConfig()
	if err != nil {
		return LogError(log, fmt.Errorf("getting env config: %w", err))
	}
	log = log.With("nodeName", envConfig.NodeName)

	// MANAGER
	mgr, err := newManager(ctx, log, envConfig)
	if err != nil {
		return err
	}

	// CONTROLLERS
	GoForever("controller", cancel, log,
		func() error { return runController(ctx, log, mgr, envConfig.NodeName) },
	)

	<-ctx.Done()

	return context.Cause(ctx)
}

func newManager(
	ctx context.Context,
	log *slog.Logger,
	envConfig *EnvConfig,
) (manager.Manager, error) {
	config, err := config.GetConfig()
	if err != nil {
		return nil, LogError(log, fmt.Errorf("getting rest config: %w", err))
	}

	scheme, err := newScheme()
	if err != nil {
		return nil, LogError(log, fmt.Errorf("building scheme: %w", err))
	}

	mgrOpts := manager.Options{
		Scheme:                 scheme,
		BaseContext:            func() context.Context { return ctx },
		Logger:                 logr.FromSlogHandler(log.Handler()),
		HealthProbeBindAddress: envConfig.HealthProbeBindAddress,
		Metrics: server.Options{
			BindAddress: envConfig.MetricsBindAddress,
		},
	}

	mgr, err := manager.New(config, mgrOpts)
	if err != nil {
		return nil, LogError(log, fmt.Errorf("creating manager: %w", err))
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, LogError(log, fmt.Errorf("AddHealthzCheck: %w", err))
	}

	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, LogError(log, fmt.Errorf("AddReadyzCheck: %w", err))
	}

	return mgr, nil
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	var schemeFuncs = []func(s *runtime.Scheme) error{
		corev1.AddToScheme,
		storagev1.AddToScheme,
		v1alpha2.AddToScheme,
	}

	for i, f := range schemeFuncs {
		if err := f(scheme); err != nil {
			return nil, fmt.Errorf("adding scheme %d: %w", i, err)
		}
	}

	return scheme, nil
}
