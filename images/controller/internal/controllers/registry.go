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
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
	rvrcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_controller"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
)

// BuildAll builds all controllers.
// podNamespace is the namespace where the controller pod runs, used by controllers
// that need to access other pods in this namespace (e.g., agent pods).
// schedulerExtenderURL is the URL of the scheduler extender service, used by the
// rvr-scheduling-controller to query LVG scores.
// isEnabled is a filter function: if it returns false for a controller name,
// that controller is skipped. When ENABLED_CONTROLLERS env is not set,
// isEnabled returns true for all names (all controllers are started).
func BuildAll(mgr manager.Manager, podNamespace string, schedulerExtenderURL string, isEnabled func(string) bool) error {
	log := mgr.GetLogger().WithName("controller-registry")

	// Must be first: controllers rely on MatchingFields against these indexes.
	if err := RegisterIndexes(mgr); err != nil {
		return fmt.Errorf("building indexes: %w", err)
	}

	type builder struct {
		name  string
		build func(mgr manager.Manager) error
	}
	builders := []builder{
		{name: rvcontroller.RVControllerName, build: rvcontroller.BuildController},
		{name: rvrschedulingcontroller.RVRSchedulingControllerName, build: func(mgr manager.Manager) error {
			return rvrschedulingcontroller.BuildController(mgr, schedulerExtenderURL)
		}},
		{name: rsccontroller.RSCControllerName, build: rsccontroller.BuildController},
		{name: nodecontroller.NodeControllerName, build: nodecontroller.BuildController},
		{name: rspcontroller.RSPControllerName, build: func(mgr manager.Manager) error {
			return rspcontroller.BuildController(mgr, podNamespace)
		}},
		{name: rvrcontroller.RVRControllerName, build: func(mgr manager.Manager) error {
			return rvrcontroller.BuildController(mgr, podNamespace)
		}},
	}

	for _, b := range builders {
		if !isEnabled(b.name) {
			log.Info("controller disabled, skipping", "controller", b.name)
			continue
		}
		log.Info("building controller", "controller", b.name)
		if err := b.build(mgr); err != nil {
			return fmt.Errorf("building controller %s: %w", b.name, err)
		}
	}

	return nil
}
