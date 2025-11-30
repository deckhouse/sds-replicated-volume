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

type NodeIDManager interface {
	NewNodeID() (uint, error)
}

type nodeIDManager struct {
	occupiedNodeIDs map[uint]struct{}
}

var _ NodeIDManager = &nodeIDManager{}

func (m *nodeIDManager) ReserveNodeID(nodeID uint) error {
	var added bool
	if m.occupiedNodeIDs, added = cmaps.SetUnique(m.occupiedNodeIDs, nodeID, struct{}{}); !added {
		return errInvalidCluster("duplicate nodeId: %d", nodeID)
	}

	return nil
}

func (m *nodeIDManager) FreeNodeID(nodeID uint) {
	delete(m.occupiedNodeIDs, nodeID)
}

func (m *nodeIDManager) NewNodeID() (nodeID uint, err error) {
	m.occupiedNodeIDs, nodeID, err = cmaps.SetLowestUnused(m.occupiedNodeIDs, uint(0), MaxNodeID)

	if err != nil {
		return 0, errInvalidCluster("unable to allocate new node id: %w", err)
	}

	return
}
