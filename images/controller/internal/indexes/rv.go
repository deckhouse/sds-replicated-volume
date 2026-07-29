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

const (
	// IndexFieldRVByReplicatedStorageClassName is used to quickly list
	// ReplicatedVolume objects referencing a specific RSC.
	IndexFieldRVByReplicatedStorageClassName = "spec.replicatedStorageClassName"

	// IndexFieldRVByStoragePoolName is used to quickly list
	// ReplicatedVolume objects using a specific RSP.
	IndexFieldRVByStoragePoolName = "status.configuration.replicatedStoragePoolName"

	// IndexFieldRVByDataSourceVolumeName is used to quickly list
	// ReplicatedVolume objects whose spec.dataSource points at another
	// ReplicatedVolume (kind=ReplicatedVolume) with the given name.
	IndexFieldRVByDataSourceVolumeName = "spec.dataSource.volumeName"
)

// RegisterRVByReplicatedStorageClassName registers the index for listing
// ReplicatedVolume objects by spec.replicatedStorageClassName.
func RegisterRVByReplicatedStorageClassName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByReplicatedStorageClassName,
		func(obj client.Object) []string {
			rv, ok := obj.(*v1alpha1.ReplicatedVolume)
			if !ok {
				return nil
			}
			if rv.Spec.ReplicatedStorageClassName == "" {
				return nil
			}
			return []string{rv.Spec.ReplicatedStorageClassName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by spec.replicatedStorageClassName: %w", err)
	}
	return nil
}

// RegisterRVByStoragePoolName registers the index for listing
// ReplicatedVolume objects by status.configuration.storagePoolName.
func RegisterRVByStoragePoolName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByStoragePoolName,
		func(obj client.Object) []string {
			rv, ok := obj.(*v1alpha1.ReplicatedVolume)
			if !ok {
				return nil
			}
			if rv.Status.Configuration == nil {
				return nil
			}
			return []string{rv.Status.Configuration.ReplicatedStoragePoolName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by status.configuration.replicatedStoragePoolName: %w", err)
	}
	return nil
}

// RegisterRVByDataSourceVolumeName registers the index for listing
// ReplicatedVolume objects cloned from a specific source RV (i.e. whose
// spec.dataSource.kind == ReplicatedVolume and spec.dataSource.name matches).
func RegisterRVByDataSourceVolumeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolume{},
		IndexFieldRVByDataSourceVolumeName,
		func(obj client.Object) []string {
			rv, ok := obj.(*v1alpha1.ReplicatedVolume)
			if !ok {
				return nil
			}
			if rv.Spec.DataSource == nil {
				return nil
			}
			if rv.Spec.DataSource.Kind != v1alpha1.VolumeDataSourceKindReplicatedVolume {
				return nil
			}
			if rv.Spec.DataSource.Name == "" {
				return nil
			}
			return []string{rv.Spec.DataSource.Name}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolume by spec.dataSource (kind=ReplicatedVolume).name: %w", err)
	}
	return nil
}
