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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// IndexFieldNodeByMetadataName is used to quickly look up
// a Node by its metadata.name.
const IndexFieldNodeByMetadataName = "metadata.name"

// RegisterNodeByMetadataName registers the index for looking up
// Node objects by metadata.name.
func RegisterNodeByMetadataName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.Node{},
		IndexFieldNodeByMetadataName,
		func(obj client.Object) []string {
			return []string{obj.GetName()}
		},
	); err != nil {
		return fmt.Errorf("index Node by metadata.name: %w", err)
	}
	return nil
}
