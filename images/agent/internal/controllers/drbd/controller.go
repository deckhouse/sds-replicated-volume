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
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

// BuildController creates and registers the DRBD controller and scanner with the manager.
func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return err
	}

	log := slog.Default().With("name", ControllerName)
	nodeName := cfg.NodeName()

	rec := NewReconciler(
		mgr.GetClient(),
		log,
		nodeName,
	)

	// Create event channel for scanner to trigger reconciliations
	// Buffer size allows scanner to queue events without blocking
	eventCh := make(chan event.GenericEvent, 100)

	// Create scanner
	scanner := NewScanner(
		mgr.GetClient(),
		log,
		nodeName,
		ScannerInterval,
		eventCh,
	)

	// Build controller with watches on both DRBDResource and the scanner's event channel
	err = builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&v1alpha1.DRBDResource{},
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
					dr := e.Object.(*v1alpha1.DRBDResource)
					return dr.Spec.NodeName == nodeName
				},
				UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
					dr := e.ObjectNew.(*v1alpha1.DRBDResource)
					return dr.Spec.NodeName == nodeName
				},
				DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
					dr := e.Object.(*v1alpha1.DRBDResource)
					return dr.Spec.NodeName == nodeName
				},
				GenericFunc: func(e event.TypedGenericEvent[client.Object]) bool {
					dr := e.Object.(*v1alpha1.DRBDResource)
					return dr.Spec.NodeName == nodeName
				},
			}),
		).
		// Watch for events from the scanner
		WatchesRawSource(
			source.Channel(eventCh, &handler.EnqueueRequestForObject{}),
		).
		Complete(rec)
	if err != nil {
		return u.LogError(log, err)
	}

	// Add scanner as a runnable
	if err := mgr.Add(scanner); err != nil {
		return u.LogError(log, err)
	}

	return nil
}
