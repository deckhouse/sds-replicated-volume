package cluster2

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

type rvrAdapter struct {
}

type RVRAdapter interface {
	Port() uint
	Minor() *uint
	Disk() string
}

var _ RVRAdapter = &rvrAdapter{}

func NewRVRAdapter(rvr *v1alpha2.ReplicatedVolumeReplica) *rvrAdapter {
	rvrA := &rvrAdapter{}

	// if rvr.Spec.NodeId > uint(MaxNodeId) {
	// 	return errInvalidCluster("expected rvr.spec.nodeId to be in range [0;%d], got %d", MaxNodeId, rvr.Spec.NodeId)
	// }

	// if len(rvr.Spec.Volumes) > 1 {
	// 	return errInvalidCluster(
	// 		"expected len(spec.volumes) <= 1, got %d for %s",
	// 		len(rvr.Spec.Volumes), rvr.Name,
	// 	)
	// }

	return rvrA
}

// Port implements RVRAdapter.
func (r *rvrAdapter) Port() uint {
	panic("unimplemented")
}

// Disk implements RVRAdapter.
func (r *rvrAdapter) Disk() string {
	panic("unimplemented")
}

// Minor implements RVRAdapter.
func (r *rvrAdapter) Minor() *uint {
	panic("unimplemented")
}
