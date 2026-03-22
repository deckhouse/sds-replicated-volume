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

package controlplanemigration

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
		OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
		Queue:        "modules/" + consts.ModuleName,
	},
	computeInternalNewControlPlane,
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

func computeInternalNewControlPlane(ctx context.Context, input *pkg.HookInput) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook panicked: %v", r)
		}
	}()

	userNewControlPlane := input.Values.Get("sdsReplicatedVolume.newControlPlane").Bool()

	var internalNewControlPlane bool

	if userNewControlPlane {
		internalNewControlPlane = true
	} else {
		cl := input.DC.MustGetK8sClient()

		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "storage.deckhouse.io",
			Version: "v1alpha1",
			Kind:    "ReplicatedVolumeList",
		})

		if err := cl.List(ctx, list, &client.ListOptions{Limit: 1}); err != nil {
			if meta.IsNoMatchError(err) {
				input.Logger.Info("ReplicatedVolume CRD not installed, defaulting newControlPlane to false")
			} else {
				return fmt.Errorf("list ReplicatedVolume resources: %w", err)
			}
		} else if len(list.Items) > 0 {
			internalNewControlPlane = true
			input.Logger.Info("found ReplicatedVolume resources, enabling new control plane")
		}
	}

	input.Values.Set("sdsReplicatedVolume.internal.newControlPlane", internalNewControlPlane)
	input.Logger.Info("computed internal newControlPlane value", "value", internalNewControlPlane)

	return nil
}

func syncControlPlaneMigrationState(_ context.Context, input *pkg.HookInput) error {
	stateList, err := objectpatch.UnmarshalToStruct[string](input.Snapshots, snapshotName)
	if err != nil {
		return fmt.Errorf("unmarshal %s snapshot: %w", snapshotName, err)
	}

	if len(stateList) == 0 {
		return nil
	}

	state := stateList[0]
	if state == "" {
		state = defaultMigrationState
	}

	input.Values.Set("sdsReplicatedVolume.internal.controlPlaneMigration", state)
	input.Logger.Info("synced control-plane migration state", "state", state)

	return nil
}
