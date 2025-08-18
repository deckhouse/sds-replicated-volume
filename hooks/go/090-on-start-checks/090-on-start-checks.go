/*
Copyright 2024 Flant JSC

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

package onstartchecks

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
)

var _ = registry.RegisterFunc(
	&pkg.HookConfig{
		OnAfterHelm: &pkg.OrderedConfig{Order: 10},
	},
	onStartChecks,
)

func onStartChecks(ctx context.Context, input *pkg.HookInput) error {
	cl := input.DC.MustGetK8sClient()

	crdGroup := "internal.linstor.linbit.com"
	crdVersion := "v1-15-0"

	propsList := &unstructured.UnstructuredList{}
	propsList.SetGroupVersionKind(
		schema.GroupVersionKind{
			Group:   crdGroup,
			Version: crdVersion,
			Kind:    "PropsContainerList",
		},
	)

	if err := cl.List(ctx, propsList); err != nil {
		return fmt.Errorf("listing propscontainers: %w", err)
	}

	thinPoolExistence := false
	for _, item := range propsList.Items {
		spec, found, _ := unstructured.NestedMap(item.Object, "spec")
		if !found {
			continue
		}
		propKey, _, _ := unstructured.NestedString(spec, "prop_key")
		propValue, _, _ := unstructured.NestedString(spec, "prop_value")
		if propKey == "DrbdOptions/AutoEvictAllowEviction" && propValue == "True" {
			patch := map[string]interface{}{
				"spec": map[string]interface{}{
					"prop_value": "False",
				},
			}
			patchObj := &unstructured.Unstructured{Object: patch}
			patchObj.SetGroupVersionKind(item.GroupVersionKind())
			patchObj.SetName(item.GetName())
			if err := cl.Patch(ctx, patchObj, client.MergeFrom(&item)); err != nil {
				input.Logger.Info("Failed to patch propscontainer", "name", item.GetName(), "err", err)
			} else {
				input.Logger.Info("Replaced DrbdOptions/AutoEvictAllowEviction value to False", "name", item.GetName())
			}
		}
		if propKey == "StorDriver/internal/lvmthin/thinPoolGranularity" {
			thinPoolExistence = true
		}
	}

	if thinPoolExistence {
		patch := map[string]interface{}{
			"spec": map[string]interface{}{
				"settings": map[string]interface{}{
					"enableThinProvisioning": true,
				},
			},
		}
		modCfg := &unstructured.Unstructured{Object: patch}
		modCfg.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "deckhouse.io",
			Version: "v1alpha1",
			Kind:    "ModuleConfig",
		})
		modCfg.SetName("sds-replicated-volume")
		// Use empty object as base for MergeFrom
		base := &unstructured.Unstructured{}
		base.SetGroupVersionKind(modCfg.GroupVersionKind())
		base.SetName(modCfg.GetName())
		if err := cl.Patch(ctx, modCfg, client.MergeFrom(base)); err != nil {
			input.Logger.Info("Failed to patch moduleconfig for thin provisioning", "err", err)
		} else {
			input.Logger.Info("Thin pools present, switching enableThinProvisioning on")
		}
	}

	return nil
}
