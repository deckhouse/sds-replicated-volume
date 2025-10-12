package cluster

import (
	"errors"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

var MaxNodeId = uint(7)

type ExistingRVRManager struct {
	occupiedNodeIds map[uint]struct{}
}

var _ NodeIdManager = &ExistingRVRManager{}

func NewExistingRVRManager(existingRVRs []v1alpha2.ReplicatedVolumeReplica) *ExistingRVRManager {
	res := &ExistingRVRManager{
		occupiedNodeIds: make(map[uint]struct{}, len(existingRVRs)),
	}
	for i := range existingRVRs {
		res.occupiedNodeIds[existingRVRs[i].Spec.NodeId] = struct{}{}
	}
	return res
}

func (e *ExistingRVRManager) ReserveNodeId() (uint, error) {
	for nodeId := uint(0); nodeId <= MaxNodeId; nodeId++ {
		if _, ok := e.occupiedNodeIds[nodeId]; !ok {
			e.occupiedNodeIds[nodeId] = struct{}{}
			return nodeId, nil
		}
	}
	return 0, errors.New("unable to allocate new node id")
}
