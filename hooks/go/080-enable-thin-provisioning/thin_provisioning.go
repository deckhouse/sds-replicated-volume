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
	v1aplha1 "github.com/deckhouse/sds-replicated-volume/api"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"os"
)

var (
	_ = registry.RegisterFunc(
		&pkg.HookConfig{
			Metadata:          pkg.HookMetadata{},
			Schedule:          nil,
			Kubernetes:        nil,
			OnStartup:         nil,
			OnBeforeHelm:      nil,
			OnAfterHelm:       nil,
			OnAfterDeleteHelm: nil,
			AllowFailure:      false,
			Queue:             "",
			Settings:          nil,
		},
		mainHook,
	)
)

func mainHook(ctx context.Context, input *pkg.HookInput, obj metav1.Object) error {

	SRVModuleConfig := *v1alpha1.
	config, err := rest.InClusterConfig()
	if err != nil {
		_, err := fmt.Fprintf(os.Stderr, "Failed to load Kubernetes config: %v\n", err)
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		_, err := fmt.Fprintf(os.Stderr, "Failed to create dynamic client: %v\n", err)
		return err
	}

	list, err := dynamicClient.Resource(v1aplha1.propsContainerGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		_, err := fmt.Fprintf(os.Stderr, "Failed to list custom objects: %v\n", err)
		return err
	}

	thinPoolExistence := false
	for _, item := range list.Items {
		propKey, found, err := unstructured.NestedString(item.Object, "spec", "prop_key")
		if err != nil || !found {
			continue
		}
		if propKey == "StorDriver/internal/lvmthin/thinPoolGranularity" {
			thinPoolExistence = true
		}
	}

	if thinPoolExistence {
		resp, err := dynamicClient.Resource(schema.GroupVersionResource{Group: "deckhouse.io", Version: "v1alpha1", Resource: "moduleconfigs"}).Patch(
			context.Background(),
			"sds-local-volume",
			"application/apply-patch+yaml",
			[]byte(`apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: sds-local-volume
spec:
  version: 1
  settings:
    enableThinProvisioning: true
`),
			metav1.PatchOptions{FieldManager: "sds-hook"},
		)

		if err != nil {
			_, err := fmt.Fprintf(os.Stderr, "Failed to patch moduleconfigs/sds-local-volume: %v\n", err)
			return err
		}

		_, err = json.Marshal(resp.Object)
		if err != nil {
			_, err := fmt.Fprintf(os.Stderr, "Failed to format response as YAML: %v\n", err)
			return err
		}
	}
	return nil
}
