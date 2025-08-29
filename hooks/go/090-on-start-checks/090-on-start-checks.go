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

	// List propscontainers
	propsList := &unstructured.UnstructuredList{}
	propsList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "internal.linstor.linbit.com",
		Version: "v1-15-0",
		Kind:    "PropsContainerList",
	})

	if err := cl.List(ctx, propsList); err != nil {
		input.Logger.Info("Failed to list propscontainers", "err", err)
		return nil // Don't fail the hook, just log and continue
	}

	thinPoolExistence := false
	patchedCount := 0

	for i := range propsList.Items {
		item := &propsList.Items[i]

		// Get spec safely
		spec, found, _ := unstructured.NestedMap(item.Object, "spec")
		if !found {
			continue
		}

		propKey, _, _ := unstructured.NestedString(spec, "prop_key")
		propValue, _, _ := unstructured.NestedString(spec, "prop_value")

		// Handle AutoEvictAllowEviction
		if propKey == "DrbdOptions/AutoEvictAllowEviction" && propValue == "True" {
			// Create a copy of the item for patching
			itemCopy := item.DeepCopy()

			// Update the prop_value
			if err := unstructured.SetNestedField(itemCopy.Object, "False", "spec", "prop_value"); err != nil {
				input.Logger.Info("Failed to set prop_value", "name", item.GetName(), "err", err)
				continue
			}

			// Apply the update
			if err := cl.Update(ctx, itemCopy); err != nil {
				input.Logger.Info("Failed to update propscontainer", "name", item.GetName(), "err", err)
			} else {
				input.Logger.Info("Updated AutoEvictAllowEviction to False", "name", item.GetName())
				patchedCount++
			}
		}

		// Check for thin pool granularity
		if propKey == "StorDriver/internal/lvmthin/thinPoolGranularity" {
			thinPoolExistence = true
		}
	}

	input.Logger.Info("Propscontainer processing complete", "total", len(propsList.Items), "patched", patchedCount)

	// Handle thin provisioning setting
	if thinPoolExistence {
		// Try to get existing ModuleConfig
		modCfg := &unstructured.Unstructured{}
		modCfg.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "deckhouse.io",
			Version: "v1alpha1",
			Kind:    "ModuleConfig",
		})
		modCfg.SetName("sds-replicated-volume")

		err := cl.Get(ctx, client.ObjectKey{Name: "sds-replicated-volume"}, modCfg)
		if err != nil {
			// Create new ModuleConfig
			newModCfg := &unstructured.Unstructured{}
			newModCfg.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "deckhouse.io",
				Version: "v1alpha1",
				Kind:    "ModuleConfig",
			})
			newModCfg.SetName("sds-replicated-volume")
			newModCfg.SetUnstructuredContent(map[string]interface{}{
				"spec": map[string]interface{}{
					"settings": map[string]interface{}{
						"enableThinProvisioning": true,
					},
				},
			})

			if err := cl.Create(ctx, newModCfg); err != nil {
				input.Logger.Info("Failed to create moduleconfig", "err", err)
			} else {
				input.Logger.Info("Created moduleconfig with thin provisioning enabled")
			}
		} else {
			// Update existing ModuleConfig
			modCfgCopy := modCfg.DeepCopy()

			// Get existing settings or create new ones
			settings, _, _ := unstructured.NestedMap(modCfgCopy.Object, "spec", "settings")
			if settings == nil {
				settings = make(map[string]interface{})
			}
			settings["enableThinProvisioning"] = true

			if err := unstructured.SetNestedField(modCfgCopy.Object, settings, "spec", "settings"); err != nil {
				input.Logger.Info("Failed to set settings", "err", err)
			} else if err := cl.Update(ctx, modCfgCopy); err != nil {
				input.Logger.Info("Failed to update moduleconfig", "err", err)
			} else {
				input.Logger.Info("Updated moduleconfig with thin provisioning enabled")
			}
		}
	} else {
		input.Logger.Info("No thin pool granularity found, skipping thin provisioning enablement")
	}

	return nil
}
