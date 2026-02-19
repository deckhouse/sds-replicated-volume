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
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	propsList := &unstructured.UnstructuredList{}
	propsList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "internal.linstor.linbit.com",
		Version: "v1-15-0",
		Kind:    "PropsContainersList",
	})

	if err := cl.List(ctx, propsList); err != nil {
		input.Logger.Info("Failed to list propscontainers", "err", err)
		return nil
	}

	patchedCount := 0

	for i := range propsList.Items {
		item := &propsList.Items[i]

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

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				input.Logger.Info("Failed to marshal patch for propscontainer", "name", item.GetName(), "err", err)
			} else {
				if err := cl.Patch(ctx, item, client.RawPatch(types.MergePatchType, patchBytes)); err != nil {
					input.Logger.Error("Failed to patch propscontainer", "name", item.GetName(), "err", err)
				} else {
					input.Logger.Info("Patched AutoEvictAllowEviction to False", "name", item.GetName())
					patchedCount++
				}
			}
		}
	}

	input.Logger.Info("Propscontainer processing complete", "total", len(propsList.Items), "patched", patchedCount)

	return nil
}
