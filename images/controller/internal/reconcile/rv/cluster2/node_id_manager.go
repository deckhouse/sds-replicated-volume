package cluster2

import (
	"errors"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type nodeIdManager struct {
	occupiedNodeIds map[uint]struct{}
}

var _ NodeIdManager = &nodeIdManager{}

func NewNodeIdManager(existingRVRs []*v1alpha2.ReplicatedVolumeReplica) (*nodeIdManager, error) {
	res := &nodeIdManager{
		occupiedNodeIds: make(map[uint]struct{}),
	}
	for _, rvr := range existingRVRs {
		if err := res.addRVR(rvr); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (m *nodeIdManager) addRVR(rvr *v1alpha2.ReplicatedVolumeReplica) error {
	if rvr.Spec.NodeId > uint(MaxNodeId) {
		return fmt.Errorf("expected rvr.spec.nodeId to be in range [0;%d], got %d", MaxNodeId, rvr.Spec.NodeId)
	}

	nodeId := rvr.Spec.NodeId

	if _, ok := m.occupiedNodeIds[nodeId]; ok {
		return fmt.Errorf("duplicate node id: %d", nodeId)
	}

	m.occupiedNodeIds[nodeId] = struct{}{}
	return nil
}

func (m *nodeIdManager) ReserveNodeId() (uint, error) {
	for nodeId := uint(0); nodeId <= MaxNodeId; nodeId++ {
		if _, ok := m.occupiedNodeIds[nodeId]; ok {
			continue
		}
		m.occupiedNodeIds[nodeId] = struct{}{}
		return nodeId, nil
	}
	return 0, errors.New("unable to allocate new node id")
}
