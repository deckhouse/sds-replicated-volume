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

package drbd

import (
	"context"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

// BuildController creates and registers the DRBD controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	log := slog.Default()
	nodeName := cfg.NodeName()

	// Create event channel for scanner to trigger reconciliations
	// Buffer size allows scanner to queue events without blocking
	eventCh := make(chan event.GenericEvent, 100)

	// Create scanner
	scanner := NewScanner(
		cl,
		log,
		nodeName,
		eventCh,
	)

	// Add scanner as a runnable
	if err := mgr.Add(scanner); err != nil {
		return fmt.Errorf("adding scanner runnable: %w", err)
	}

	// Create port cache (reconciler-owned)
	portCache := NewPortCache(context.Background(), PortRangeMin, PortRangeMax)

	// Create reconciler
	rec := NewReconciler(
		cl,
		nodeName,
		portCache,
	)

	// Build controller
	return builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&v1alpha1.DRBDResource{},
			builder.WithPredicates(drbdrPredicates(nodeName)...),
		).
		// Watch for events from the scanner
		WatchesRawSource(
			source.Channel(eventCh, &handler.EnqueueRequestForObject{}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}
