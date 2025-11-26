package main

import (
	"context"
	"fmt"
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func runControllers(
	ctx context.Context,
	log *slog.Logger,
	mgr manager.Manager,
) error {
	if err := controllers.BuildAll(mgr); err != nil {
		return err
	}
	if err := mgr.Start(ctx); err != nil {
		return u.LogError(log, fmt.Errorf("starting controller: %w", err))
	}

	return ctx.Err()
}
