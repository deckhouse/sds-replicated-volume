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

package drbdprimary

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

const (
	controllerName = "drbd_primary_controller"
)

func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return err
	}
	r := &Reconciler{
		cl:     mgr.GetClient(),
		log:    mgr.GetLogger().WithName(controllerName).WithName("Reconciler"),
		scheme: mgr.GetScheme(),
		cfg:    cfg,
	}

	return builder.ControllerManagedBy(mgr).
		Named(controllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{},
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
					return thisNodeRVRShouldEitherBePromotedOrDemotedOrHasErrors(
						cfg.NodeName(),
						e.Object.(*v1alpha1.ReplicatedVolumeReplica),
					)
				},
				UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
					return thisNodeRVRShouldEitherBePromotedOrDemotedOrHasErrors(
						cfg.NodeName(),
						e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica),
					)
				},
				DeleteFunc: func(event.TypedDeleteEvent[client.Object]) bool {
					return false
				},
				GenericFunc: func(event.TypedGenericEvent[client.Object]) bool {
					return false
				},
			})).
		Complete(r)
}

func thisNodeRVRShouldEitherBePromotedOrDemotedOrHasErrors(nodeName string, rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Spec.NodeName != nodeName {
		// not this node
		return false
	}

	wantPrimary, actuallyPrimary, initialized := rvrDesiredAndActualRole(rvr)
	if !initialized {
		// not ready for promote/demote
		return false
	}

	if wantPrimary == actuallyPrimary && allErrorsAreNil(rvr) {
		// do not need promote/demote and has no errors
		return false
	}

	return true
}
