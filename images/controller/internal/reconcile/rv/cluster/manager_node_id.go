/*
Copyright 2025 Flant JSC

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

package cluster

import (
	cmaps "github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

type NodeIdManager interface {
	NewNodeId() (uint, error)
}

type nodeIdManager struct {
	occupiedNodeIds map[uint]struct{}
}

var _ NodeIdManager = &nodeIdManager{}

func (m *nodeIdManager) ReserveNodeId(nodeId uint) error {
	var added bool
	if m.occupiedNodeIds, added = cmaps.SetUnique(m.occupiedNodeIds, nodeId, struct{}{}); !added {
		return errInvalidCluster("duplicate nodeId: %d", nodeId)
	}

	return nil
}

func (m *nodeIdManager) FreeNodeId(nodeId uint) {
	delete(m.occupiedNodeIds, nodeId)
}

func (m *nodeIdManager) NewNodeId() (nodeId uint, err error) {
	m.occupiedNodeIds, nodeId, err = cmaps.SetLowestUnused(m.occupiedNodeIds, uint(0), MaxNodeId)

	if err != nil {
		return 0, errInvalidCluster("unable to allocate new node id: %w", err)
	}

	return
}
