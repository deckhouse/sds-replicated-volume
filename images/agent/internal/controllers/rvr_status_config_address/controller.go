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

package rvrstatusconfigaddress

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return err
	}

	const controllerName = "rvr-status-config-address-controller"

	log := mgr.GetLogger().WithName(controllerName)
	var rec = NewReconciler(mgr.GetClient(), log, cfg)

	return builder.ControllerManagedBy(mgr).
		Named(controllerName).
		// We reconciling nodes as single unit to make sure we will not assign the same port because of race condition.
		// We are not watching node updates because internalIP we are using is not expected to change
		// For(&corev1.Node{}, builder.WithPredicates(NewNodePredicate(cfg.NodeName, log))).
		Watches(
			&v1alpha1.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(EnqueueNodeByRVRFunc(cfg.NodeName(), log)),
			builder.WithPredicates(SkipWhenRVRNodeNameNotUpdatedPred(log)),
		).
		Complete(rec)
}
