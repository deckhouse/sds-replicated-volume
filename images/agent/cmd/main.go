/*
Copyright 2026 Flant JSC

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

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/deckhouse/sds-common-lib/slogh"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
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

const drbdUsermodeHelperPath = "/sys/module/drbd/parameters/usermode_helper"

func disableDRBDUsermodeHelper(log *slog.Logger) {
	// MUST be exactly "disabled" without a trailing newline: DRBD's
	// drbd_maybe_khelper short-circuits on strcmp(drbd_usermode_helper,
	// "disabled") == 0 (see 3p-drbd drbd/drbd_nl.c). The kernel's
	// param_set_copystring is a raw strcpy and preserves whatever we
	// write, so a trailing '\n' breaks the equality test, the
	// short-circuit doesn't fire, every helper invocation reaches
	// call_usermodehelper("disabled\n", ...), fails with -ENOENT, and
	// the kernel logs a KERN_WARNING per invocation.
	if err := os.WriteFile(drbdUsermodeHelperPath, []byte("disabled"), 0o644); err != nil {
		log.Warn("failed to disable DRBD usermode helper", "path", drbdUsermodeHelperPath, "err", err)
	} else {
		log.Info("disabled DRBD usermode helper", "path", drbdUsermodeHelperPath)
	}
}

func run(ctx context.Context, log *slog.Logger) (err error) {
	// Detect capabilities first: it runs "drbdsetup status", which loads and
	// initializes the DRBD kernel module when /sys/module/drbd is absent. The
	// usermode-helper write below targets /sys/module/drbd and needs it present.
	if err := drbdutils.DetectCapabilities(ctx); err != nil {
		log.Warn("DRBD capability detection: drbdsetup status failed", "err", err)
	}
	log.Info("DRBD capabilities detected", "flantExtensions", drbdutils.FlantExtensionsSupported)

	disableDRBDUsermodeHelper(log)

	// The derived Context is canceled the first time a function passed to eg.Go
	// returns a non-nil error or the first time Wait returns
	eg, ctx := errgroup.WithContext(ctx)

	envConfig, err := env.GetConfig()
	if err != nil {
		return u.LogError(log, err)
	}
	log = log.With("nodeName", envConfig.NodeName())

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

	return eg.Wait()
}
