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

package controllers

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

// RegisterIndexes registers controller-runtime cache indexes used by controllers.
// It must be invoked before any controller starts listing with MatchingFields.
func RegisterIndexes(mgr manager.Manager) error {
	// Index ReplicatedVolumeAttachment by spec.replicatedVolumeName for efficient lookups per RV.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeAttachment{},
		indexes.IndexFieldRVAByReplicatedVolumeName,
		func(obj client.Object) []string {
			rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
			if !ok {
				return nil
			}
			if rva.Spec.ReplicatedVolumeName == "" {
				return nil
			}
			return []string{rva.Spec.ReplicatedVolumeName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeAttachment by spec.replicatedVolumeName: %w", err)
	}

	// Index ReplicatedVolumeReplica by spec.nodeName for efficient lookups per node.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		indexes.IndexFieldRVRByNodeName,
		func(obj client.Object) []string {
			rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok {
				return nil
			}
			if rvr.Spec.NodeName == "" {
				return nil
			}
			return []string{rvr.Spec.NodeName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeReplica by spec.nodeName: %w", err)
	}
	// Index ReplicatedVolumeReplica by spec.replicatedVolumeName for efficient lookups per RV.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		indexes.IndexFieldRVRByReplicatedVolumeName,
		func(obj client.Object) []string {
			rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok {
				return nil
			}
			if rvr.Spec.ReplicatedVolumeName == "" {
				return nil
			}
			return []string{rvr.Spec.ReplicatedVolumeName}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeReplica by spec.replicatedVolumeName: %w", err)
	}

	return nil
}
