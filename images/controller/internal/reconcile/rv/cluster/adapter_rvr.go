package cluster

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type rvrAdapter struct {
	rvr *v1alpha2.ReplicatedVolumeReplica
}

type RVRAdapter interface {
	Name() string
	NodeName() string
	Port() uint
	// -1 for diskless rvr
	Minor() int
	// empty string for diskless rvr
	Disk() string
	NodeId() uint
	Size() int

	// Reconcile(rvNode RVNodeAdapter, props RVRTargetPropsAdapter) (RequiredAction, error)
}

var _ RVRAdapter = &rvrAdapter{}

func NewRVRAdapter(rvr *v1alpha2.ReplicatedVolumeReplica) (*rvrAdapter, error) {
	if rvr == nil {
		return nil, errArgNil("rvr")
	}

	rvr = rvr.DeepCopy()

	if len(rvr.Spec.Volumes) > 1 {
		return nil,
			errInvalidCluster(
				"expected rvr to have no more then 1 volume, '%s' got %d",
				rvr.Name, len(rvr.Spec.Volumes),
			)
	}

	if len(rvr.Spec.Volumes) > 0 {
		if rvr.Spec.Volumes[0].Device > MaxNodeMinor {
			return nil,
				errInvalidCluster(
					"expected rvr device minor to be not more then %d, got %d",
					MaxNodeMinor, rvr.Spec.Volumes[0].Device,
				)
		}
	}

	if rvr.Status != nil && rvr.Status.DRBD != nil {
		if len(rvr.Status.DRBD.Devices) > 1 {
			return nil,
				errInvalidCluster(
					"expected rvr to have no more then 1 device in status, '%s' got %d",
					rvr.Name, len(rvr.Status.DRBD.Devices),
				)
		}
	}

	return &rvrAdapter{rvr: rvr}, nil
}

func (r *rvrAdapter) Name() string {
	return r.rvr.Name
}

func (r *rvrAdapter) NodeName() string {
	return r.rvr.Spec.NodeName
}

func (r *rvrAdapter) Port() uint {
	return r.rvr.Spec.NodeAddress.Port
}

func (r *rvrAdapter) Disk() string {
	if len(r.rvr.Spec.Volumes) > 0 {
		return r.rvr.Spec.Volumes[0].Disk
	}
	return ""
}

func (r *rvrAdapter) Minor() int {
	if len(r.rvr.Spec.Volumes) > 0 {

		return int(r.rvr.Spec.Volumes[0].Device)
	}
	return -1
}

func (r *rvrAdapter) NodeId() uint {
	return r.rvr.Spec.NodeId
}

func (r *rvrAdapter) Size() int {
	var size int
	if r.rvr.Status != nil && r.rvr.Status.DRBD != nil && len(r.rvr.Status.DRBD.Devices) > 0 {
		size = r.rvr.Status.DRBD.Devices[0].Size
	}
	return size
}
