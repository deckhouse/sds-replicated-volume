package cluster2

import "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"

type rvrAdapter struct {
}

type RVRAdapter interface {
	Name() string
	NodeName() string
	Port() uint
	Minor() *uint
	Disk() string
	NodeId() uint

	Size() int64
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

// Name implements RVRAdapter.
func (r *rvrAdapter) Name() string {
	panic("unimplemented")
}

// NodeName implements RVRAdapter.
func (r *rvrAdapter) NodeName() string {
	panic("unimplemented")
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

// NodeId implements RVRAdapter.
func (r *rvrAdapter) NodeId() uint {
	panic("unimplemented")
}

// Size implements RVRAdapter.
func (r *rvrAdapter) Size() int64 {
	panic("unimplemented")
}
