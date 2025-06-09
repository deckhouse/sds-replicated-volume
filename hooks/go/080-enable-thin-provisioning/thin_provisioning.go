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

package thinprovisioning

import (
	"context"
	"fmt"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-replicated-volume/api/linstor"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	_ = registry.RegisterFunc(
		&pkg.HookConfig{
			OnAfterHelm: &pkg.OrderedConfig{Order: 15},
			Queue:       fmt.Sprintf("modules/%s", consts.ModuleName),
		},
		mainHook,
	)
)

func mainHook(ctx context.Context, input *pkg.HookInput) error {

	var mc = &v1alpha1.ModuleConfig{}
	cl := input.DC.MustGetK8sClient()
	list := linstor.PropsContainersList{}

	thinPoolExistence := false
	for _, item := range list.Items {
		if item.Spec.PropKey == "StorDriver/internal/lvmthin/thinPoolGranularity" {
			thinPoolExistence = true
		}
	}

	if thinPoolExistence {
		if value, exists := mc.Spec.Settings["enableThinProvisioning"]; exists && value == true {
			klog.Info("Thin provisioning is already enabled, nothing to do here")
			return nil
		} else {
			klog.Info("Enabling thin provisioning support")
			patchBytes, err := json.Marshal(map[string]interface{}{
				"spec": map[string]interface{}{
					"version": 1,
					"settings": map[string]interface{}{
						"enableThinProvisioning": true,
					},
				},
			})

			if err != nil {
				klog.Fatalf("Error marshalling patch: %s", err.Error())
			}

			err = cl.Patch(context.TODO(), mc, client.RawPatch(types.MergePatchType, patchBytes))
			if err != nil {
				klog.Fatalf("Error patching object: %s", err.Error())
			}
		}
	}
	return nil
}
