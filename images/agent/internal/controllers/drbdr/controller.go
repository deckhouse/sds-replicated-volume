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
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/ctrlexec"
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
		return err
	}

	cl := mgr.GetClient()
	nodeName := cfg.NodeName()

	// drbdsetup/drbdmeta can hang indefinitely on backing-device I/O
	// (most commonly when an LV underneath enters uninterruptible
	// D-state); bare exec would leak the reconcile goroutine forever.
	// The one-shot factory wraps each call so the reconcile context
	// stays cancellable (WithCache), the underlying process is bounded
	// (WithTimeout), and Cmd.Wait survives pipe-holding grandchildren
	// (ConfigureCmd sets WaitDelay).
	//
	// events2 streams via StdoutPipe+Start+Wait and runs for the
	// manager lifetime — neither WithCache (CombinedOutput-only) nor a
	// per-call timeout fits — so it uses a bare factory and relies on
	// ConfigureCmd alone for pipe-orphan safety.
	drbdutils.ExecCommandContext = withDRBDCommandLogging(
		ctrlexec.WithCache(
			ctrlexec.WithTimeout(drbdutils.ExecTimeout,
				ctrlexec.ExecCommandContext(drbdutils.ConfigureCmd))))
	drbdutils.Events2ExecCommandContext = withDRBDCommandLogging(
		ctrlexec.ExecCommandContext(drbdutils.ConfigureCmd))

	// Create internal request channel (scanner sends here)
	requestCh := make(chan event.TypedGenericEvent[DRBDReconcileRequest], 100)

	// Create DRBD port cache (scanner-maintained, will be used by PortRegistry later)
	drbdPortCache := NewDRBDPortCache()

	// Create scanner with port cache
	scanner := NewScanner(requestCh, drbdPortCache)
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	// Create port registry (reconciler-owned, uses DRBDPortCache for kernel state)
	portRegistry := NewPortRegistry(cl, nodeName, drbdPortCache, cfg.DRBDMinPort(), cfg.DRBDMaxPort(), 10*time.Minute)

	// Create reconciler (implements reconcile.TypedReconciler[DRBDReconcileRequest])
	rec := NewReconciler(cl, nodeName, portRegistry)

	// Build DRBD resource controller with TypedReconciler
	if err := builder.TypedControllerManagedBy[DRBDReconcileRequest](mgr).
		Named(ControllerName).
		WithLogConstructor(func(req *DRBDReconcileRequest) logr.Logger {
			l := mgr.GetLogger().WithValues(
				"controller", ControllerName,
				"controllerGroup", v1alpha1.APIGroup,
				"controllerKind", "DRBDResource",
			)
			if req != nil {
				name := req.Name
				if name == "" {
					name = req.ActualNameOnTheNode
				}
				l = l.WithValues("name", name)
			}
			return l
		}).
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
			MaxConcurrentReconciles: 20,
			RateLimiter:             controlleroptions.DefaultRateLimiter[DRBDReconcileRequest](),
		}).
		Complete(withDurationLogging(rec)); err != nil {
		return fmt.Errorf("building DRBD resource controller: %w", err)
	}

	return nil
}

// withDRBDCommandLogging wraps a factory so every drbd command invocation
// is logged at info level on the per-call context's logger.
func withDRBDCommandLogging(factory ctrlexec.ExecCommandContextFactory) ctrlexec.ExecCommandContextFactory {
	return func(ctx context.Context, name string, args ...string) ctrlexec.Cmd {
		log.FromContext(ctx).Info("executing drbd command", "command", name, "args", args)
		return factory(ctx, name, args...)
	}
}

func withDurationLogging(inner reconcile.TypedReconciler[DRBDReconcileRequest]) reconcile.TypedReconciler[DRBDReconcileRequest] {
	return reconcile.TypedFunc[DRBDReconcileRequest](func(ctx context.Context, req DRBDReconcileRequest) (reconcile.Result, error) {
		start := time.Now()
		res, err := inner.Reconcile(ctx, req)
		log.FromContext(ctx).Info("Reconcile complete", "duration", time.Since(start).String())
		return res, err
	})
}
