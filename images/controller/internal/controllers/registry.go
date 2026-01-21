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

package controllers

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	nodecontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/node_controller"
	rsccontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rsc_controller"
	rvattachcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_attach_controller"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
	rvdeletepropagation "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_delete_propagation"
	rvrcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_controller"
	rvrmetadata "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_metadata"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
	rvrvolume "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_volume"
	worldcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/world_controller"
)

func BuildAll(mgr manager.Manager) error {
	// Must be first: controllers rely on MatchingFields against these indexes.
	if err := RegisterIndexes(mgr); err != nil {
		return fmt.Errorf("registering indexes: %w", err)
	}

	worldGate, worldBus, err := worldcontroller.BuildController(mgr)
	if err != nil {
		return fmt.Errorf("building world_controller: %w", err)
	}

	if err := rsccontroller.BuildController(mgr, worldGate, worldBus); err != nil {
		return fmt.Errorf("building rsc_controller: %w", err)
	}

	if err := rvcontroller.BuildController(mgr, worldGate, worldBus); err != nil {
		return fmt.Errorf("building rv_controller: %w", err)
	}

	if err := rvrcontroller.BuildController(mgr, worldGate, worldBus); err != nil {
		return fmt.Errorf("building rvr_controller: %w", err)
	}

	if err := rvrschedulingcontroller.BuildController(mgr, worldGate); err != nil {
		return fmt.Errorf("building rvr_scheduling_controller: %w", err)
	}

	if err := nodecontroller.BuildController(mgr); err != nil {
		return fmt.Errorf("building node_controller: %w", err)
	}

	// Old controllers
	if err := rvrtiebreakercount.BuildController(mgr); err != nil {
		return fmt.Errorf("building rvr_tie_breaker_count controller: %w", err)
	}
	if err := rvrvolume.BuildController(mgr); err != nil {
		return fmt.Errorf("building rvr_volume controller: %w", err)
	}
	if err := rvrmetadata.BuildController(mgr); err != nil {
		return fmt.Errorf("building rvr_metadata controller: %w", err)
	}
	if err := rvdeletepropagation.BuildController(mgr); err != nil {
		return fmt.Errorf("building rv_delete_propagation controller: %w", err)
	}
	if err := rvattachcontroller.BuildController(mgr); err != nil {
		return fmt.Errorf("building rv_attach_controller: %w", err)
	}

	return nil
}
