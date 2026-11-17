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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/deckhouse/sds-common-lib/logger"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-common-lib/webhookserver"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/webhooks/handlers"
)

func main() {
	ctx := signals.SetupSignalHandler()

	// LOG_LEVEL, the numeric 0..4 value lib-helm emits, in place of the slogh
	// ConfigMap this app used to reload at runtime: that file carried a hard-coded
	// level=INFO and never saw sdsReplicatedVolume.logLevel, so the module setting
	// did nothing for this binary.
	baseLog, err := logger.NewLoggerFromEnv(logger.InfoLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create the logger: %s\n", err)
		os.Exit(1)
	}
	log := baseLog.With("startedAt", time.Now().Format(time.RFC3339))
	slog.SetDefault(slog.New(log.Handler()))

	crlog.SetLogger(log.GetLogger())

	log.Info("controller app started")

	err = run(ctx, log)
	if !errors.Is(err, context.Canceled) || ctx.Err() != context.Canceled {
		log.Error("exited unexpectedly", "err", err, "ctxerr", ctx.Err())
		os.Exit(1)
	}
	log.Info(
		"gracefully shutdown",
		// cleanup errors do not affect status code, but worth logging
		"err", err,
	)
}

func run(ctx context.Context, log logger.Logger) (err error) {
	// The derived Context is canceled the first time a function passed to eg.Go
	// returns a non-nil error or the first time Wait returns
	eg, ctx := errgroup.WithContext(ctx)

	envConfig, err := env.GetConfig()
	if err != nil {
		return err
	}

	// MANAGER
	mgr, err := newManager(ctx, log, envConfig)
	if err != nil {
		return err
	}

	webhookOpts, webhooksEnabled, err := webhookserver.OptionsFromEnv()
	if err != nil {
		return u.LogError(log, fmt.Errorf("reading the webhook server configuration: %w", err))
	}
	if webhooksEnabled {
		hs, err := handlers.Handlers(log.Named("webhooks"))
		if err != nil {
			return u.LogError(log, fmt.Errorf("creating the webhook handlers: %w", err))
		}
		if err := webhookserver.Add(mgr, webhookOpts, hs); err != nil {
			return u.LogError(log, fmt.Errorf("adding the webhook server: %w", err))
		}
		log.Info("serving the admission webhooks", "port", webhookOpts.Port, "certDir", webhookOpts.CertDir)
	}

	eg.Go(func() error {
		if err := mgr.Start(ctx); err != nil {
			return u.LogError(log, fmt.Errorf("starting controller: %w", err))
		}
		return ctx.Err()
	})

	// ...

	return eg.Wait()
}
