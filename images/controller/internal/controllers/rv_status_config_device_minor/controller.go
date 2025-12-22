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

package rvstatusconfigdeviceminor

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func BuildController(mgr manager.Manager) error {
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(RVStatusConfigDeviceMinorControllerName).WithName("Reconciler"),
	)
	// MaxConcurrentReconciles: 1
	// prevents race conditions when assigning unique deviceMinor values
	// to different ReplicatedVolume resources. Status not protected by optimistic locking,
	// so we need to prevent parallel reconciles for avoiding duplicate assignments.

	return builder.ControllerManagedBy(mgr).
		Named(RVStatusConfigDeviceMinorControllerName).
		For(&v1alpha1.ReplicatedVolume{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(rec)
}
