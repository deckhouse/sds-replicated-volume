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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// RVRByRVNameAndNodeName indexes ReplicatedVolumeReplica by composite key
	// of spec.replicatedVolumeName and spec.nodeName.
	RVRByRVNameAndNodeName = "spec.replicatedVolumeName+spec.nodeName"
)

// RVRByRVNameAndNodeNameKey returns the index key for the composite index.
func RVRByRVNameAndNodeNameKey(rvName, nodeName string) string {
	return rvName + "/" + nodeName
}

// RegisterIndexes registers all field indexes used by the agent.
func RegisterIndexes(ctx context.Context, mgr manager.Manager) error {
	indexer := mgr.GetFieldIndexer()

	// Index by composite key: spec.replicatedVolumeName + spec.nodeName
	if err := indexer.IndexField(
		ctx,
		&v1alpha1.ReplicatedVolumeReplica{},
		RVRByRVNameAndNodeName,
		func(rawObj client.Object) []string {
			replica := rawObj.(*v1alpha1.ReplicatedVolumeReplica)
			if replica.Spec.ReplicatedVolumeName == "" || replica.Spec.NodeName == "" {
				return nil
			}
			return []string{RVRByRVNameAndNodeNameKey(replica.Spec.ReplicatedVolumeName, replica.Spec.NodeName)}
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", RVRByRVNameAndNodeName, err)
	}

	return nil
}
