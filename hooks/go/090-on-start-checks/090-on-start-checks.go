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
	"github.com/deckhouse/module-sdk/pkg/app"
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
	logger := input.Logger

	// Define constants
	const (
		crdGroup    = "internal.linstor.linbit.com"
		crdVersion  = "v1-15-0"
		crdKind     = "PropsContainerList"
		moduleName  = "sds-replicated-volume"
	)

	// Create GVR for propscontainers
	gvr := schema.GroupVersionResource{
		Group:    crdGroup,
		Version:  crdVersion,
		Resource: "propscontainers",
	}

	// List propscontainers
	propsList := &unstructured.UnstructuredList{}
	propsList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   crdGroup,
		Version: crdVersion,
		Kind:    crdKind,
	})

	if err := cl.List(ctx, propsList); err != nil {
		return fmt.Errorf("failed to list propscontainers: %w", err)
	}

	logger.Info("processing propscontainers", "count", len(propsList.Items))

	thinPoolExistence := false
	patchedCount := 0

	// Process each propscontainer
	for _, item := range propsList.Items {
		spec, found, _ := unstructured.NestedMap(item.Object, "spec")
		if !found {
			logger.Debug("propscontainer missing spec", "name", item.GetName())
			continue
		}

		propKey, _, _ := unstructured.NestedString(spec, "prop_key")
		propValue, _, _ := unstructured.NestedString(spec, "prop_value")

		// Handle AutoEvictAllowEviction
		if propKey == "DrbdOptions/AutoEvictAllowEviction" && propValue == "True" {
			if err := patchPropsContainer(ctx, cl, &item, "False"); err != nil {
				logger.Error(err, "failed to patch propscontainer", "name", item.GetName())
			} else {
				logger.Info("patched AutoEvictAllowEviction", "name", item.GetName(), "value", "False")
				patchedCount++
			}
		}

		// Check for thin pool granularity
		if propKey == "StorDriver/internal/lvmthin/thinPoolGranularity" {
			thinPoolExistence = true
			logger.Debug("found thin pool granularity setting", "name", item.GetName())
		}
	}

	logger.Info("propscontainer processing complete", "total", len(propsList.Items), "patched", patchedCount)

	// Handle thin provisioning setting
	if thinPoolExistence {
		if err := enableThinProvisioning(ctx, cl, moduleName); err != nil {
			logger.Error(err, "failed to enable thin provisioning")
		} else {
			logger.Info("enabled thin provisioning", "module", moduleName)
		}
	} else {
		logger.Debug("thin pool granularity not found, skipping thin provisioning enablement")
	}

	return nil
}

// patchPropsContainer patches a propscontainer with new prop_value
func patchPropsContainer(ctx context.Context, cl client.Client, item *unstructured.Unstructured, newValue string) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"prop_value": newValue,
		},
	}
	
	patchObj := &unstructured.Unstructured{Object: patch}
	patchObj.SetGroupVersionKind(item.GroupVersionKind())
	patchObj.SetName(item.GetName())
	
	return cl.Patch(ctx, patchObj, client.MergeFrom(item))
}

// enableThinProvisioning enables thin provisioning in module config
func enableThinProvisioning(ctx context.Context, cl client.Client, moduleName string) error {
	// Get existing module config
	modCfg := &unstructured.Unstructured{}
	modCfg.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ModuleConfig",
	})
	modCfg.SetName(moduleName)

	// Try to get existing config first
	if err := cl.Get(ctx, client.ObjectKey{Name: moduleName}, modCfg); err != nil {
		// If not found, create new one
		patch := map[string]interface{}{
			"spec": map[string]interface{}{
				"settings": map[string]interface{}{
					"enableThinProvisioning": true,
				},
			},
		}
		modCfg.SetUnstructuredContent(patch)
		return cl.Create(ctx, modCfg)
	}

	// Update existing config
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"settings": map[string]interface{}{
				"enableThinProvisioning": true,
			},
		},
	}
	
	patchObj := &unstructured.Unstructured{Object: patch}
	patchObj.SetGroupVersionKind(modCfg.GroupVersionKind())
	patchObj.SetName(moduleName)
	
	return cl.Patch(ctx, patchObj, client.MergeFrom(modCfg))
}

	return nil
}
