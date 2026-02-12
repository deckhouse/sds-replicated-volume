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

	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdop"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
)

// BuildAll builds all controllers.
// isEnabled is a filter function: if it returns false for a controller name,
// that controller is skipped. When ENABLED_CONTROLLERS env is not set,
// isEnabled returns true for all names (all controllers are started).
func BuildAll(mgr manager.Manager, isEnabled func(string) bool) error {
	log := mgr.GetLogger().WithName("controller-registry")

	type builder struct {
		name  string
		build func(mgr manager.Manager) error
	}
	builders := []builder{
		{name: drbdr.ControllerName, build: drbdr.BuildController},
		{name: drbdop.ControllerName, build: drbdop.BuildController},
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
