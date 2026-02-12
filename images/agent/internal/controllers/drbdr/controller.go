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

package drbdr

import (
	"context"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/controlleroptions"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// BuildController creates and registers the DRBD controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	// Register field indexes
	if err := indexes.RegisterDRBDRByNodeName(mgr); err != nil {
		return fmt.Errorf("registering DRBDR index: %w", err)
	}
	if err := indexes.RegisterLVGByNodeName(mgr); err != nil {
		return fmt.Errorf("registering LVG index: %w", err)
	}
	if err := indexes.RegisterLLVByLVGName(mgr); err != nil {
		return fmt.Errorf("registering LLV index: %w", err)
	}

	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	log := slog.Default()
	nodeName := cfg.NodeName()

	// Set up drbdsetup command logging
	origExec := drbdsetup.ExecCommandContext
	drbdsetup.ExecCommandContext = func(ctx context.Context, name string, arg ...string) drbdsetup.Cmd {
		log.Info("executing drbdsetup command", "command", name, "args", arg)
		return origExec(ctx, name, arg...)
	}

	// Create internal request channel (scanner sends here)
	requestCh := make(chan event.TypedGenericEvent[DRBDReconcileRequest], 100)

	// Create scanner with new channel type
	scanner := NewScanner(log.With("name", ScannerName), requestCh)
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	// Create port cache (reconciler-owned)
	portCache := NewPortCache(context.Background(), PortRangeMin, PortRangeMax)

	// Create reconciler (implements reconcile.TypedReconciler[DRBDReconcileRequest])
	rec := NewReconciler(cl, nodeName, portCache)

	// Build DRBD resource controller with TypedReconciler
	if err := builder.TypedControllerManagedBy[DRBDReconcileRequest](mgr).
		Named(ControllerName).
		// Watch DRBDResource and map to DRBDReconcileRequest{Name: ...}
		// Predicates already filter by nodeName, so map func just converts
		Watches(
			&v1alpha1.DRBDResource{},
			handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []DRBDReconcileRequest {
				dr := obj.(*v1alpha1.DRBDResource)
				return []DRBDReconcileRequest{{Name: dr.Name}}
			}),
			builder.WithPredicates(drbdrPredicates(nodeName)...),
		).
		// Watch internal channel (scanner events) - maps *DRBDReconcileRequest to DRBDReconcileRequest
		WatchesRawSource(
			source.TypedChannel(requestCh, handler.TypedEnqueueRequestsFromMapFunc(
				func(_ context.Context, req DRBDReconcileRequest) []DRBDReconcileRequest {
					return []DRBDReconcileRequest{req}
				},
			)),
		).
		WithOptions(controller.TypedOptions[DRBDReconcileRequest]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             controlleroptions.DefaultRateLimiter[DRBDReconcileRequest](),
		}).
		Complete(rec); err != nil {
		return fmt.Errorf("building DRBD resource controller: %w", err)
	}

	return nil
}
