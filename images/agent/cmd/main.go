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

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/deckhouse/sds-common-lib/slogh"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

func NewCl() (client.Client, error) {
	kubeConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting config: %w", err)
	}

	scheme, err := scheme.New()
	if err != nil {
		return nil, fmt.Errorf("scheme: %w", err)
	}

	clientOpts := client.Options{
		Scheme: scheme,
	}

	return client.New(kubeConfig, clientOpts)
}

func testPatch() {
	ctx := context.Background()

	cl, err := NewCl()
	if err != nil {
		panic(err)
	}

	op := &v1alpha1.DRBDResourceListOperation{}

	gvk, err := apiutil.GVKForObject(op, cl.Scheme())
	if err != nil {
		panic(err)
	}
	op.SetGroupVersionKind(gvk)

	op.SetName("drbdrlop-0")

	op.Status = &v1alpha1.DRBDResourceListOperationStatus{
		Results: []v1alpha1.DRBDResourceListOperationResult{
			{DRBDResourceName: "test1"},
		},
	}

	if err := cl.Status().Patch(ctx, op, client.Apply, client.FieldOwner("testPatch1")); err != nil {
		panic(err)
	}

}

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

	envConfig, err := env.GetConfig()
	if err != nil {
		return u.LogError(log, fmt.Errorf("getting env config: %w", err))
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
