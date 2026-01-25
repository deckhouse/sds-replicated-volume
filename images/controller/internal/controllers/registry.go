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
	rspcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rsp_controller"
	rvattachcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_attach_controller"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
	rvdeletepropagation "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_delete_propagation"
	rvrcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_controller"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
)

// BuildAll builds all controllers.
// podNamespace is the namespace where the controller pod runs, used by controllers
// that need to access other pods in this namespace (e.g., agent pods).
func BuildAll(mgr manager.Manager, podNamespace string) error {
	// Must be first: controllers rely on MatchingFields against these indexes.
	if err := RegisterIndexes(mgr); err != nil {
		return fmt.Errorf("building indexes: %w", err)
	}

	// Controllers that don't need podNamespace.
	builders := []func(mgr manager.Manager) error{
		rvrtiebreakercount.BuildController,
		rvcontroller.BuildController,
		rvdeletepropagation.BuildController,
		rvrschedulingcontroller.BuildController,
		rvrcontroller.BuildController,
		rvattachcontroller.BuildController,
		rsccontroller.BuildController,
		nodecontroller.BuildController,
	}

	for i, buildCtl := range builders {
		if err := buildCtl(mgr); err != nil {
			return fmt.Errorf("building controller %d: %w", i, err)
		}
	}

	// RSP controller needs podNamespace for agent pod discovery.
	if err := rspcontroller.BuildController(mgr, podNamespace); err != nil {
		return fmt.Errorf("building rsp controller: %w", err)
	}

	return nil
}
