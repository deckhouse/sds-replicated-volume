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

// IndexFieldRSCByStoragePool is used to quickly list
// ReplicatedStorageClass objects referencing a specific RSP (deprecated field for migration).
const IndexFieldRSCByStoragePool = "spec.storagePool"

// IndexFieldRSCByStatusStoragePoolName is used to quickly list
// ReplicatedStorageClass objects by their auto-generated RSP name.
const IndexFieldRSCByStatusStoragePoolName = "status.storagePoolName"

// RegisterRSCByStoragePool registers the index for listing
// ReplicatedStorageClass objects by spec.storagePool.
func RegisterRSCByStoragePool(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedStorageClass{},
		IndexFieldRSCByStoragePool,
		func(obj client.Object) []string {
			rsc, ok := obj.(*v1alpha1.ReplicatedStorageClass)
			if !ok {
				return nil
			}
			if rsc.Spec.StoragePool == "" {
				return nil
			}
			return []string{rsc.Spec.StoragePool}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedStorageClass by spec.storagePool: %w", err)
	}
	return nil
}

// RegisterRSCByStatusStoragePoolName registers the index for listing
// ReplicatedStorageClass objects by status.storagePoolName.
func RegisterRSCByStatusStoragePoolName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedStorageClass{},
		IndexFieldRSCByStatusStoragePoolName,
		func(obj client.Object) []string {
			rsc, ok := obj.(*v1alpha1.ReplicatedStorageClass)
			if !ok {
				return nil
			}
			if rsc.Status.StoragePoolName == "" {
				return nil
			}
			return []string{rsc.Status.StoragePoolName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedStorageClass by status.storagePoolName: %w", err)
	}
	return nil
}
