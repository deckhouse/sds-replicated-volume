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
	"golang.org/x/sync/errgroup"

	. "github.com/deckhouse/sds-common-lib/utils"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	ctx := signals.SetupSignalHandler()

	slogh.EnableConfigReload(ctx, nil)
	logHandler := &slogh.Handler{}
	log := slog.New(logHandler).
		With("startedAt", time.Now().Format(time.RFC3339))
	crlog.SetLogger(logr.FromSlogHandler(logHandler))

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
	// The derived Context is canceled the first time a function passed to eg.Go
	// returns a non-nil error or the first time Wait returns
	eg, ctx := errgroup.WithContext(ctx)

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

	cl := mgr.GetClient()

	eg.Go(func() error {
		return runController(
			ctx,
			log.With("actor", "controller"),
			mgr,
			envConfig.NodeName,
		)
	})

	// DRBD SCANNER
	scanner := NewScanner(ctx, log.With("actor", "scanner"), cl, envConfig)

	eg.Go(func() error {
		return scanner.Run()
	})

	eg.Go(func() error {
		return scanner.ConsumeBatches()
	})

	return eg.Wait()
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
		Scheme:      scheme,
		BaseContext: func() context.Context { return ctx },
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&v1alpha2.ReplicatedVolumeReplica{}: {
					// only watch current node's replicas
					Field: (&v1alpha2.ReplicatedVolumeReplica{}).
						NodeNameSelector(envConfig.NodeName),
				},
			},
		},
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

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha2.ReplicatedVolumeReplica{},
		"spec.nodeName",
		func(rawObj client.Object) []string {
			replica := rawObj.(*v1alpha2.ReplicatedVolumeReplica)
			if replica.Spec.NodeName == "" {
				return nil
			}
			return []string{replica.Spec.NodeName}
		},
	)
	if err != nil {
		return nil,
			LogError(log, fmt.Errorf("indexing %s: %w", "spec.nodeName", err))
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
