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

// WithRSPByLVMVolumeGroupNameIndex registers the IndexFieldRSPByLVMVolumeGroupName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRSPByLVMVolumeGroupNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedStoragePool{}, indexes.IndexFieldRSPByLVMVolumeGroupName, func(obj client.Object) []string {
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
	})
}

// WithRSPByEligibleNodeNameIndex registers the IndexFieldRSPByEligibleNodeName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRSPByEligibleNodeNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedStoragePool{}, indexes.IndexFieldRSPByEligibleNodeName, func(obj client.Object) []string {
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
	})
}

// WithRSPByUsedByRSCNameIndex registers the IndexFieldRSPByUsedByRSCName index
// on a fake.ClientBuilder. This is useful for tests that need to use the index.
func WithRSPByUsedByRSCNameIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedStoragePool{}, indexes.IndexFieldRSPByUsedByRSCName, func(obj client.Object) []string {
		rsp, ok := obj.(*v1alpha1.ReplicatedStoragePool)
		if !ok {
			return nil
		}
		return rsp.Status.UsedBy.ReplicatedStorageClassNames
	})
}
