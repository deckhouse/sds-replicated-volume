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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
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

	// IndexFieldRVRByResyncEndsOccupancy is used to quickly list the
	// ReplicatedVolumeReplica objects that carry resync ends on their node.
	//
	// Unlike the indexes above, its key is not a field path: syncbudget.OccupiesEnds
	// derives it from the replica's spec and status together.
	IndexFieldRVRByResyncEndsOccupancy = "syncbudget.occupiesEnds"

	// IndexValueResyncEndsOccupant is the only value
	// IndexFieldRVRByResyncEndsOccupancy ever takes: a replica carrying no ends
	// carries no key at all rather than a second value.
	IndexValueResyncEndsOccupant = "true"
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

// RegisterRVRByResyncEndsOccupancy registers the index for listing the
// ReplicatedVolumeReplica objects that carry resync ends on their node.
func RegisterRVRByResyncEndsOccupancy(mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ReplicatedVolumeReplica{},
		IndexFieldRVRByResyncEndsOccupancy,
		ExtractRVRResyncEndsOccupancy,
	); err != nil {
		return fmt.Errorf("index ReplicatedVolumeReplica by syncbudget.occupiesEnds: %w", err)
	}
	return nil
}

// ExtractRVRResyncEndsOccupancy is the extractor behind IndexFieldRVRByResyncEndsOccupancy.
//
// It is exported, unlike the extractors above, so that a fake client can register the very
// function the manager registers. The predicate it wraps decides which replicas the occupancy map
// is built from, and a test-only copy of it could agree with this one today and not tomorrow.
func ExtractRVRResyncEndsOccupancy(obj client.Object) []string {
	rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
	if !ok {
		return nil
	}
	if !syncbudget.OccupiesEnds(rvr) {
		return nil
	}
	return []string{IndexValueResyncEndsOccupant}
}
