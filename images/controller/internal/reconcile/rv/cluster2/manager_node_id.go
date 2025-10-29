package cluster2

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
