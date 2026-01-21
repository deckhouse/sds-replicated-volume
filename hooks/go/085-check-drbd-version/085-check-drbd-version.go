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
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	drbdVersionsConfigMapName = "d8-sds-replicated-volume-drbd-versions"
	allowedDrbdVersionsKey    = "allowedDrbdVersions"
	moduleConfigName          = "sds-replicated-volume"
)

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

	cm := &corev1.ConfigMap{}
	if err := cl.Get(ctx, types.NamespacedName{Name: drbdVersionsConfigMapName, Namespace: consts.ModuleNamespace}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("configmap %q not found in namespace %q", drbdVersionsConfigMapName, consts.ModuleNamespace)
		}
		return fmt.Errorf("failed to get configmap %q in namespace %q: %w", drbdVersionsConfigMapName, consts.ModuleNamespace, err)
	}

	allowedVersions := parseAllowedVersions(cm.Data[allowedDrbdVersionsKey])
	if len(allowedVersions) == 0 {
		return fmt.Errorf("configmap %q has empty %q list", drbdVersionsConfigMapName, allowedDrbdVersionsKey)
	}

	if _, ok := allowedVersions[drbdVersion]; !ok {
		return fmt.Errorf("drbdVersion %q is not in allowed list from %q", drbdVersion, drbdVersionsConfigMapName)
	}

	input.Logger.Info("DRBD version check passed", "drbdVersion", drbdVersion)
	return nil
}

func parseAllowedVersions(raw string) map[string]struct{} {
	versions := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ','
	}) {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		versions[v] = struct{}{}
	}
	return versions
}
