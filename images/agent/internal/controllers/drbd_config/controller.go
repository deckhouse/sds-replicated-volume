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

package drbdconfig

import (
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scanner"
)

func BuildController(mgr manager.Manager) error {
	cfg, err := env.GetConfig()
	if err != nil {
		return err
	}

	log := slog.Default().With("name", ControllerName)

	rec := NewReconciler(
		mgr.GetClient(),
		log,
		cfg.NodeName(),
		scanner.DefaultScanner(),
	)

	return u.LogError(
		log,
		builder.ControllerManagedBy(mgr).
			Named(ControllerName).
			For(&v1alpha1.ReplicatedVolume{}).
			Watches(
				&v1alpha1.ReplicatedVolumeReplica{},
				handler.EnqueueRequestForOwner(
					mgr.GetScheme(),
					mgr.GetRESTMapper(),
					&v1alpha1.ReplicatedVolume{},
				),
			).
			Complete(rec))
}
