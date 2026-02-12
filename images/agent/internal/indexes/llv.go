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

// IndexFieldLLVByLVGName is used to quickly list
// LVMLogicalVolume objects for a specific LVMVolumeGroup.
const IndexFieldLLVByLVGName = "spec.lvmVolumeGroupName"

// RegisterLLVByLVGName registers the index for listing
// LVMLogicalVolume objects by spec.lvmVolumeGroupName.
func RegisterLLVByLVGName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&snc.LVMLogicalVolume{},
		IndexFieldLLVByLVGName,
		func(obj client.Object) []string {
			llv, ok := obj.(*snc.LVMLogicalVolume)
			if !ok || llv.Spec.LVMVolumeGroupName == "" {
				return nil
			}
			return []string{llv.Spec.LVMVolumeGroupName}
		},
	); err != nil {
		return fmt.Errorf("index LVMLogicalVolume by spec.lvmVolumeGroupName: %w", err)
	}
	return nil
}
