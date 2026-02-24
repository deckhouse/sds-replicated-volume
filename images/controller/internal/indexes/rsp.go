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

// IndexFieldRSPByLVMVolumeGroupName is used to quickly list
// ReplicatedStoragePool objects referencing a specific LVMVolumeGroup.
// The index extracts all LVG names from spec.lvmVolumeGroups[*].name.
const IndexFieldRSPByLVMVolumeGroupName = "spec.lvmVolumeGroups.name"

// RegisterRSPByLVMVolumeGroupName registers the index for listing
// ReplicatedStoragePool objects by spec.lvmVolumeGroups[*].name.
func RegisterRSPByLVMVolumeGroupName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedStoragePool{},
		IndexFieldRSPByLVMVolumeGroupName,
		func(obj client.Object) []string {
			rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
			if !ok {
				return nil
			}
			if len(rsp.Spec.LVMVolumeGroups) == 0 {
				return nil
			}
			names := make([]string, 0, len(rsp.Spec.LVMVolumeGroups))
			for _, lvg := range rsp.Spec.LVMVolumeGroups {
				if lvg.Name != "" {
					names = append(names, lvg.Name)
				}
			}
			return names
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedStoragePool by spec.lvmVolumeGroups.name: %w", err)
	}
	return nil
}

// IndexFieldRSPByUsedByRSCName is used to quickly list
// ReplicatedStoragePool objects that are used by a specific RSC.
// The index extracts all RSC names from status.usedBy.replicatedStorageClassNames.
const IndexFieldRSPByUsedByRSCName = "status.usedBy.replicatedStorageClassNames"

// RegisterRSPByUsedByRSCName registers the index for listing
// ReplicatedStoragePool objects by status.usedBy.replicatedStorageClassNames.
func RegisterRSPByUsedByRSCName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedStoragePool{},
		IndexFieldRSPByUsedByRSCName,
		func(obj client.Object) []string {
			rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
			if !ok {
				return nil
			}
			return rsp.Status.UsedBy.ReplicatedStorageClassNames
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedStoragePool by status.usedBy.replicatedStorageClassNames: %w", err)
	}
	return nil
}

// IndexFieldRSPByEligibleNodeName is used to quickly list
// ReplicatedStoragePool objects that have a specific node in their EligibleNodes.
// The index extracts all node names from status.eligibleNodes[*].nodeName.
const IndexFieldRSPByEligibleNodeName = "status.eligibleNodes.nodeName"

// RegisterRSPByEligibleNodeName registers the index for listing
// ReplicatedStoragePool objects by status.eligibleNodes[*].nodeName.
func RegisterRSPByEligibleNodeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedStoragePool{},
		IndexFieldRSPByEligibleNodeName,
		func(obj client.Object) []string {
			rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
			if !ok {
				return nil
			}
			if len(rsp.Status.EligibleNodes) == 0 {
				return nil
			}
			names := make([]string, 0, len(rsp.Status.EligibleNodes))
			for _, en := range rsp.Status.EligibleNodes {
				if en.NodeName != "" {
					names = append(names, en.NodeName)
				}
			}
			return names
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedStoragePool by status.eligibleNodes.nodeName: %w", err)
	}
	return nil
}
