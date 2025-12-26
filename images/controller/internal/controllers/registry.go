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

	rvdeletepropagation "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_delete_propagation"
	rvfinalizer "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_finalizer"
	rvpublishcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_publish_controller"
	rvstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_conditions"
	rvstatusconfigdeviceminor "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_device_minor"
	rvstatusconfigquorum "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
	rvstatusreplicas "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_replicas"
	rvraccesscount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_access_count"
	rvrdiskfulcount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_diskful_count"
	rvrfinalizerrelease "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_finalizer_release"
	rvrownerreference "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_owner_reference"
	rvrschedulingcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_scheduling_controller"
	rvrstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_conditions"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	rvrstatusconfigpeers "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_peers"
	rvrtiebreakercount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_tie_breaker_count"
	rvrvolume "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_volume"
)

var registry = []func(mgr manager.Manager) error{}

func init() {
	registry = append(registry, rvrdiskfulcount.BuildController)
	registry = append(registry, rvrtiebreakercount.BuildController)
	registry = append(registry, rvstatusconfigquorum.BuildController)
	registry = append(registry, rvrstatusconfigpeers.BuildController)
	registry = append(registry, rvrstatusconfignodeid.BuildController)
	registry = append(registry, rvstatusconfigdeviceminor.BuildController)
	registry = append(registry, rvstatusconfigsharedsecret.BuildController)
	registry = append(registry, rvraccesscount.BuildController)
	registry = append(registry, rvrvolume.BuildController)
	registry = append(registry, rvrownerreference.BuildController)
	registry = append(registry, rvdeletepropagation.BuildController)
	registry = append(registry, rvrfinalizerrelease.BuildController)
	registry = append(registry, rvfinalizer.BuildController)
	registry = append(registry, rvrstatusconditions.BuildController)
	registry = append(registry, rvstatusconditions.BuildController)
	registry = append(registry, rvstatusreplicas.BuildController)
	registry = append(registry, rvrschedulingcontroller.BuildController)
	registry = append(registry, rvpublishcontroller.BuildController)

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
