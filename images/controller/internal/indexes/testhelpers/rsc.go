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

package testhelpers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// WithRSCByStoragePoolIndex registers the IndexFieldRSCByStoragePool index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRSCByStoragePoolIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedStorageClass{}, indexes.IndexFieldRSCByStoragePool, func(obj client.Object) []string {
		rsc, ok := obj.(*v1alpha1.ReplicatedStorageClass)
		if !ok {
			return nil
		}
		if rsc.Spec.StoragePool == "" {
			return nil
		}
		return []string{rsc.Spec.StoragePool}
	})
}

// WithRSCByStatusStoragePoolNameIndex registers the IndexFieldRSCByStatusStoragePoolName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRSCByStatusStoragePoolNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedStorageClass{}, indexes.IndexFieldRSCByStatusStoragePoolName, func(obj client.Object) []string {
		rsc, ok := obj.(*v1alpha1.ReplicatedStorageClass)
		if !ok {
			return nil
		}
		if rsc.Status.StoragePoolName == "" {
			return nil
		}
		return []string{rsc.Status.StoragePoolName}
	})
}
