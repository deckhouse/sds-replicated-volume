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

package checkdrbdversion

import (
	"context"
	"fmt"
	"strings"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const moduleConfigName = "sds-replicated-volume"

var allowedDrbdVersions = map[string]struct{}{
	"9.2.13": {},
	"9.2.16": {},
}

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		OnBeforeHelm: &pkg.OrderedConfig{Order: 5},
	},
	onBeforeHelmChecks,
)

func onBeforeHelmChecks(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	modCfg := &unstructured.Unstructured{}
	modCfg.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ModuleConfig",
	})
	modCfg.SetName(moduleConfigName)

	if err := cl.Get(ctx, client.ObjectKey{Name: moduleConfigName}, modCfg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return fmt.Errorf("failed to get ModuleConfig %q: %w", moduleConfigName, err)
	}

	drbdVersion, found, _ := unstructured.NestedString(modCfg.Object, "spec", "settings", "drbdVersion")
	if !found || strings.TrimSpace(drbdVersion) == "" {
		return nil
	}

	if len(allowedDrbdVersions) == 0 {
		return fmt.Errorf("allowed drbd versions list is empty")
	}

	if _, ok := allowedDrbdVersions[drbdVersion]; !ok {
		return fmt.Errorf("drbdVersion %q is not in allowed list", drbdVersion)
	}

	input.Logger.Info("DRBD version check passed", "drbdVersion", drbdVersion)
	return nil
}
