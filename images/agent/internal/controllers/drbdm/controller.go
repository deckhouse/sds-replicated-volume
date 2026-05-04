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

package drbdm

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/dmsetup"
)

// BuildController creates and registers the DRBD mapper controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	if err := indexes.RegisterDRBDMByNodeName(mgr); err != nil {
		return fmt.Errorf("registering DRBDM index: %w", err)
	}

	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	nodeName := cfg.NodeName()

	origExec := dmsetup.ExecCommandContext
	dmsetup.ExecCommandContext = func(ctx context.Context, name string, arg ...string) dmsetup.Cmd {
		log.FromContext(ctx).Info("executing command", "command", name, "args", arg)
		return origExec(ctx, name, arg...)
	}

	requestCh := make(chan event.TypedGenericEvent[reconcile.Request], 100)

	scanner := NewScanner(requestCh)
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	rec := NewReconciler(cl, nodeName)

	if err := builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(&v1alpha1.DRBDMapper{}, builder.WithPredicates(drbdmPredicates(nodeName)...)).
		WatchesRawSource(
			source.TypedChannel(requestCh, handler.TypedEnqueueRequestsFromMapFunc(
				func(_ context.Context, req reconcile.Request) []reconcile.Request {
					return []reconcile.Request{req}
				},
			)),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter[reconcile.Request](),
		}).
		Complete(rec); err != nil {
		return fmt.Errorf("building DRBD mapper controller: %w", err)
	}

	return nil
}
