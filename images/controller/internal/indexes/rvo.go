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

// IndexFieldRVOByRSCOwnerRef is used to quickly list
// ReplicatedVolumeOperation objects owned by a specific RSC.
const IndexFieldRVOByRSCOwnerRef = "metadata.ownerReferences.rsc"

// RegisterRVOByRSCOwnerRef registers the index for listing
// ReplicatedVolumeOperation objects by RSC owner reference.
func RegisterRVOByRSCOwnerRef(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeOperation{},
		IndexFieldRVOByRSCOwnerRef,
		func(obj client.Object) []string {
			rvo, ok := obj.(*v1alpha1.ReplicatedVolumeOperation)
			if !ok {
				return nil
			}
			for _, ref := range rvo.OwnerReferences {
				if ref.APIVersion == v1alpha1.SchemeGroupVersion.String() &&
					ref.Kind == "ReplicatedStorageClass" {
					return []string{ref.Name}
				}
			}
			return nil
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeOperation by RSC ownerRef: %w", err)
	}
	return nil
}

// IndexFieldRVOByRVOwnerRef is used to quickly list
// ReplicatedVolumeOperation objects owned by a specific RV.
const IndexFieldRVOByRVOwnerRef = "metadata.ownerReferences.rv"

// RegisterRVOByRVOwnerRef registers the index for listing
// ReplicatedVolumeOperation objects by RV owner reference.
func RegisterRVOByRVOwnerRef(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeOperation{},
		IndexFieldRVOByRVOwnerRef,
		func(obj client.Object) []string {
			rvo, ok := obj.(*v1alpha1.ReplicatedVolumeOperation)
			if !ok {
				return nil
			}
			for _, ref := range rvo.OwnerReferences {
				if ref.APIVersion == v1alpha1.SchemeGroupVersion.String() &&
					ref.Kind == "ReplicatedVolume" {
					return []string{ref.Name}
				}
			}
			return nil
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeOperation by RV ownerRef: %w", err)
	}
	return nil
}
