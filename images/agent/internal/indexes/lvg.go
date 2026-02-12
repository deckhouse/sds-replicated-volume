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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// IndexFieldLVGByNodeName is used to quickly list
// LVMVolumeGroup objects on a specific node.
const IndexFieldLVGByNodeName = "status.nodes[0].name"

// RegisterLVGByNodeName registers the index for listing
// LVMVolumeGroup objects by status.nodes[0].name.
func RegisterLVGByNodeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&snc.LVMVolumeGroup{},
		IndexFieldLVGByNodeName,
		func(obj client.Object) []string {
			lvg, ok := obj.(*snc.LVMVolumeGroup)
			if !ok || len(lvg.Status.Nodes) == 0 {
				return nil
			}
			return []string{lvg.Status.Nodes[0].Name}
		},
	); err != nil {
		return fmt.Errorf("index LVMVolumeGroup by status.nodes[0].name: %w", err)
	}
	return nil
}
