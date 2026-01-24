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

package rvrcontroller

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const RVRControllerName = "rvr-controller"

func BuildController(mgr manager.Manager) error {
	cl := mgr.GetClient()

	rec := NewReconciler(cl, mgr.GetLogger().WithName(RVRControllerName))

	return builder.ControllerManagedBy(mgr).
		Named(RVRControllerName).
		For(&v1alpha1.ReplicatedVolumeReplica{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(rec)
}
