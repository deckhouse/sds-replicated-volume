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

package controlplanemigrationstatesync

import (
	"context"

	"github.com/deckhouse/module-sdk/pkg"
	objectpatch "github.com/deckhouse/module-sdk/pkg/object-patch"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/module-sdk/pkg/utils/ptr"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

const (
	snapshotName          = "control-plane-migration-state"
	controlPlaneCMName    = "control-plane-migration"
	defaultMigrationState = "not_started"
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		Kubernetes: []pkg.KubernetesConfig{
			{
				Name:                         snapshotName,
				APIVersion:                   "v1",
				Kind:                         "ConfigMap",
				JqFilter:                     ".data.state // \"\"",
				ExecuteHookOnSynchronization: ptr.Bool(true),
				ExecuteHookOnEvents:          ptr.Bool(true),
				NamespaceSelector: &pkg.NamespaceSelector{
					NameSelector: &pkg.NameSelector{
						MatchNames: []string{consts.ModuleNamespace},
					},
				},
				NameSelector: &pkg.NameSelector{
					MatchNames: []string{controlPlaneCMName},
				},
			},
		},
		Queue: "modules/" + consts.ModuleName,
	},
	syncControlPlaneMigrationState,
)

func syncControlPlaneMigrationState(_ context.Context, input *pkg.HookInput) error {
	stateList, err := objectpatch.UnmarshalToStruct[string](input.Snapshots, snapshotName)
	if err != nil {
		return err
	}

	state := defaultMigrationState
	if len(stateList) > 0 && stateList[0] != "" {
		state = stateList[0]
	}

	input.Values.Set("sdsReplicatedVolume.internal.controlPlaneMigration", state)
	input.Logger.Info("synced control-plane migration state", "state", state)

	return nil
}
