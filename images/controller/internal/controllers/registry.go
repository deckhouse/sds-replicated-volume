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

	rvattachcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_attach_controller"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
	rvdeletepropagation "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_delete_propagation"
	rvstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_conditions"
	rvstatusconfigquorum "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
	rvraccesscount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_access_count"
	rvrdiskfulcount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_diskful_count"
	rvrfinalizerrelease "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_finalizer_release"
	rvrmetadata "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_metadata"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
	rvrstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_conditions"
	rvrstatusconfigpeers "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_peers"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
	rvrvolume "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_volume"
)

var registry = []func(mgr manager.Manager) error{}

func init() {
	// Must be first: controllers rely on MatchingFields against these indexes.
	registry = append(registry, RegisterIndexes)

	registry = append(registry, rvrdiskfulcount.BuildController)
	registry = append(registry, rvrtiebreakercount.BuildController)
	registry = append(registry, rvstatusconfigquorum.BuildController)
	registry = append(registry, rvrstatusconfigpeers.BuildController)
	registry = append(registry, rvcontroller.BuildController)
	registry = append(registry, rvstatusconfigsharedsecret.BuildController)
	registry = append(registry, rvraccesscount.BuildController)
	registry = append(registry, rvrvolume.BuildController)
	registry = append(registry, rvrmetadata.BuildController)
	registry = append(registry, rvdeletepropagation.BuildController)
	registry = append(registry, rvrfinalizerrelease.BuildController)
	registry = append(registry, rvrstatusconditions.BuildController)
	registry = append(registry, rvstatusconditions.BuildController)
	registry = append(registry, rvrschedulingcontroller.BuildController)
	registry = append(registry, rvattachcontroller.BuildController)

	// ...
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
