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

package indexes

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// IndexFieldDRBDResourceByNodeName is used to quickly list
// DRBDResource objects on a specific node.
const IndexFieldDRBDResourceByNodeName = "spec.nodeName"

// RegisterDRBDResourceByNodeName registers the index for listing
// DRBDResource objects by spec.nodeName.
func RegisterDRBDResourceByNodeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.DRBDResource{},
		IndexFieldDRBDResourceByNodeName,
		func(obj client.Object) []string {
			dr, ok := obj.(*v1alpha1.DRBDResource)
			if !ok {
				return nil
			}
			if dr.Spec.NodeName == "" {
				return nil
			}
			return []string{dr.Spec.NodeName}
		},
	); err != nil {
		return fmt.Errorf("index DRBDResource by spec.nodeName: %w", err)
	}
	return nil
}
