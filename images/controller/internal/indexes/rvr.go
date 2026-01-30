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
	// IndexFieldRVRByNodeName is used to quickly list
	// ReplicatedVolumeReplica objects on a specific node.
	IndexFieldRVRByNodeName = "spec.nodeName"

	// IndexFieldRVRByReplicatedVolumeName is used to quickly list
	// ReplicatedVolumeReplica objects belonging to a specific RV.
	IndexFieldRVRByReplicatedVolumeName = "spec.replicatedVolumeName"

	// IndexFieldRVRByRVAndNode is a composite index for quickly finding
	// a ReplicatedVolumeReplica by both replicatedVolumeName and nodeName.
	// Key format: "<replicatedVolumeName>/<nodeName>"
	IndexFieldRVRByRVAndNode = "spec.replicatedVolumeName+nodeName"

	// IndexFieldRVRUnscheduledNonAccess is used to quickly list
	// all unscheduled non-Access ReplicatedVolumeReplica objects.
	// Query with sentinel value "true" to get all unscheduled Diskful/TieBreaker RVRs.
	IndexFieldRVRUnscheduledNonAccess = "unscheduled.nonAccess"

	// indexValueTrue is a sentinel value for boolean-like indexes.
	indexValueTrue = "true"
)

// RegisterRVRByNodeName registers the index for listing
// ReplicatedVolumeReplica objects by spec.nodeName.
func RegisterRVRByNodeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexFieldRVRByNodeName,
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
	return nil
}

// RegisterRVRByReplicatedVolumeName registers the index for listing
// ReplicatedVolumeReplica objects by spec.replicatedVolumeName.
func RegisterRVRByReplicatedVolumeName(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexFieldRVRByReplicatedVolumeName,
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

// RegisterRVRByRVAndNode registers the composite index for finding
// ReplicatedVolumeReplica objects by both replicatedVolumeName and nodeName.
func RegisterRVRByRVAndNode(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexFieldRVRByRVAndNode,
		func(obj client.Object) []string {
			rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok {
				return nil
			}
			if rvr.Spec.ReplicatedVolumeName == "" || rvr.Spec.NodeName == "" {
				return nil
			}
			return []string{RVRByRVAndNodeKey(rvr.Spec.ReplicatedVolumeName, rvr.Spec.NodeName)}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeReplica by replicatedVolumeName+nodeName: %w", err)
	}
	return nil
}

// RVRByRVAndNodeKey builds the composite key for IndexFieldRVRByRVAndNode.
func RVRByRVAndNodeKey(rvName, nodeName string) string {
	return rvName + "/" + nodeName
}

// RegisterRVRUnscheduledNonAccess registers the index for listing
// all unscheduled non-Access ReplicatedVolumeReplica objects.
// Use IndexValueRVRUnscheduledNonAccess() to query.
func RegisterRVRUnscheduledNonAccess(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexFieldRVRUnscheduledNonAccess,
		func(obj client.Object) []string {
			rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
			if !ok {
				return nil
			}
			// Only unscheduled
			if rvr.Spec.NodeName != "" {
				return nil
			}
			// Only non-Access replicas (Diskful, TieBreaker)
			if rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
				return nil
			}

			// Only RVRs that belong to a ReplicatedVolume
			if rvr.Spec.ReplicatedVolumeName == "" {
				return nil
			}
			return []string{indexValueTrue}
		},
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeReplica unscheduled non-Access: %w", err)
	}
	return nil
}

// IndexValueRVRUnscheduledNonAccess returns the sentinel value to query
// the IndexFieldRVRUnscheduledNonAccess index.
func IndexValueRVRUnscheduledNonAccess() string {
	return indexValueTrue
}
