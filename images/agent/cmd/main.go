package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/deckhouse/sds-common-lib/slogh"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	ctx := signals.SetupSignalHandler()

	slogh.EnableConfigReload(ctx, nil)
	logHandler := &slogh.Handler{}
	log := slog.New(logHandler).
		With("startedAt", time.Now().Format(time.RFC3339))
	crlog.SetLogger(logr.FromSlogHandler(logHandler))
	slog.SetDefault(log)

	log.Info("agent app started")

	err := run(ctx, log)
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

func run(ctx context.Context, log *slog.Logger) (err error) {
	// The derived Context is canceled the first time a function passed to eg.Go
	// returns a non-nil error or the first time Wait returns
	eg, ctx := errgroup.WithContext(ctx)

	envConfig, err := GetEnvConfig()
	if err != nil {
		return u.LogError(log, fmt.Errorf("getting env config: %w", err))
	}
	log = log.With("nodeName", envConfig.NodeName)

	// MANAGER
	mgr, err := newManager(ctx, log, envConfig)
	if err != nil {
		return err
	}

	eg.Go(func() error {
		if err := mgr.Start(ctx); err != nil {
			return u.LogError(log, fmt.Errorf("starting controller: %w", err))
		}
		return ctx.Err()
	})

	// DRBD SCANNER
	scanner := NewScanner(ctx, log.With("actor", "scanner"), mgr.GetClient(), envConfig)

	eg.Go(func() error {
		return scanner.Run()
	})

	eg.Go(func() error {
		return scanner.ConsumeBatches()
	})

	return eg.Wait()
}
