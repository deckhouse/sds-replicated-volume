package clustertest

import (
	"context"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv/cluster"
)

type MockRVRClient struct {
	byRVName   map[string][]v1alpha2.ReplicatedVolumeReplica
	byNodeName map[string][]v1alpha2.ReplicatedVolumeReplica
}

func NewMockRVRClient(existingRVRs []v1alpha2.ReplicatedVolumeReplica) *MockRVRClient {
	res := &MockRVRClient{
		byRVName:   map[string][]v1alpha2.ReplicatedVolumeReplica{},
		byNodeName: map[string][]v1alpha2.ReplicatedVolumeReplica{},
	}
	for _, rvr := range existingRVRs {
		res.byRVName[rvr.Spec.ReplicatedVolumeName] = append(res.byRVName[rvr.Spec.ReplicatedVolumeName], rvr)
		res.byNodeName[rvr.Spec.NodeName] = append(res.byNodeName[rvr.Spec.NodeName], rvr)
	}
	return res
}

func (m *MockRVRClient) ByReplicatedVolumeName(
	ctx context.Context,
	resourceName string,
) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	return slices.Clone(m.byRVName[resourceName]), nil
}

func (m *MockRVRClient) ByNodeName(
	ctx context.Context,
	nodeName string,
) ([]v1alpha2.ReplicatedVolumeReplica, error) {
	return slices.Clone(m.byNodeName[nodeName]), nil
}

var _ cluster.RVRClient = &MockRVRClient{}
var _ cluster.NodeRVRClient = &MockRVRClient{}
