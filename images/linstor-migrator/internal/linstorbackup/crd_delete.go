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

package linstorbackup

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
)

// DeleteCRDsForGroup deletes every CustomResourceDefinition whose spec.group equals group.
// Custom resources of those types are removed by the API server as part of CRD deletion.
func DeleteCRDsForGroup(ctx context.Context, kClient kubecl.Client, log *slog.Logger, group string) error {
	crds := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := kClient.List(ctx, crds); err != nil {
		return fmt.Errorf("list CustomResourceDefinitions: %w", err)
	}

	var names []string
	for i := range crds.Items {
		if crds.Items[i].Spec.Group != group {
			continue
		}
		names = append(names, crds.Items[i].Name)
	}
	sort.Strings(names)

	for _, name := range names {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		crd.Name = name
		if err := kClient.Delete(ctx, crd); err != nil {
			if apierrors.IsNotFound(err) {
				log.Debug("LINSTOR CRD already deleted", "name", name)
				continue
			}
			return fmt.Errorf("delete CRD %q: %w", name, err)
		}
		log.Info("deleted LINSTOR CRD", "name", name)
	}
	return nil
}
