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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/env"
)

// BuildAll builds all controllers.
// Controllers consult env.GetConfig() (cached) for any configuration they need.
// When ENABLED_CONTROLLERS env is not set, all controllers are started; otherwise
// only the listed controllers are built.
func BuildAll(mgr manager.Manager) error {
	log := mgr.GetLogger().WithName("controller-registry")

	cfg, err := env.GetConfig()
	if err != nil {
		return err
	}

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
		{name: rvrschedulingcontroller.RVRSchedulingControllerName, build: rvrschedulingcontroller.BuildController},
		{name: rsccontroller.RSCControllerName, build: rsccontroller.BuildController},
		{name: nodecontroller.NodeControllerName, build: nodecontroller.BuildController},
		{name: rspcontroller.RSPControllerName, build: rspcontroller.BuildController},
		{name: rvrcontroller.RVRControllerName, build: rvrcontroller.BuildController},
	}

	for _, b := range builders {
		if !cfg.IsControllerEnabled(b.name) {
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
