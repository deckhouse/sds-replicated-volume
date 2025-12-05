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

package rvrdiskfulcount // TODO change package if need

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	rec := &Reconciler{
		cl:  mgr.GetClient(),
		log: mgr.GetLogger().WithName("controller_rv_status_config_quorum"),
	}

	return builder.ControllerManagedBy(mgr).
		Named("rv_status_config_quorum_controller").
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&v1alpha3.ReplicatedVolume{}),
		).
		Complete(rec)
}
