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

package drbdrlop

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

const (
	// ControllerName is the stable name for the DRBD resource list operation controller.
	ControllerName = "drbd-resource-list-operation-controller"
)

// BuildController creates and registers the DRBDResourceListOperation controller with the manager.
func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return fmt.Errorf("getting config: %w", err)
	}

	cl := mgr.GetClient()
	nodeName := cfg.NodeName()

	rec := NewListOperationReconciler(cl, nodeName)
	if err := builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&v1alpha1.DRBDResourceListOperation{},
			builder.WithPredicates(listOperationPredicates(nodeName)...),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(rec); err != nil {
		return fmt.Errorf("building DRBD resource list operation controller: %w", err)
	}

	return nil
}
