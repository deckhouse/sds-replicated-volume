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

package controllers

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	rvrdiskfulcount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_diskful_count"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
)

var registry []func(mgr manager.Manager) error

func init() {
	registry = append(registry, rvrdiskfulcount.BuildController)
	registry = append(registry, rvrtiebreakercount.BuildController)

	// TODO issues/333 register new controllers here
}

func BuildAll(mgr manager.Manager) error {
	for i, buildCtl := range registry {
		err := buildCtl(mgr)
		if err != nil {
			return fmt.Errorf("building controller %d: %w", i, err)
		}
	}
	return nil
}
