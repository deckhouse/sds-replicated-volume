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

// Package testhelpers provides utilities for registering indexes with fake clients in tests.
package testhelpers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// WithRVByReplicatedStorageClassNameIndex registers the IndexFieldRVByReplicatedStorageClassName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRVByReplicatedStorageClassNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolume{}, indexes.IndexFieldRVByReplicatedStorageClassName, func(obj client.Object) []string {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok {
			return nil
		}
		if rv.Spec.ReplicatedStorageClassName == "" {
			return nil
		}
		return []string{rv.Spec.ReplicatedStorageClassName}
	})
}

// WithRVByStoragePoolNameIndex registers the IndexFieldRVByStoragePoolName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRVByStoragePoolNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolume{}, indexes.IndexFieldRVByStoragePoolName, func(obj client.Object) []string {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok {
			return nil
		}
		if rv.Status.Configuration == nil {
			return nil
		}
		return []string{rv.Status.Configuration.ReplicatedStoragePoolName}
	})
}

// WithRVByDataSourceVolumeNameIndex registers the IndexFieldRVByDataSourceVolumeName
// index on a fake.ClientBuilder. This is useful for tests that need to look up
// clone-target RVs by the source RV name (spec.dataSource, kind=ReplicatedVolume).
func WithRVByDataSourceVolumeNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolume{}, indexes.IndexFieldRVByDataSourceVolumeName, func(obj client.Object) []string {
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
	})
}
