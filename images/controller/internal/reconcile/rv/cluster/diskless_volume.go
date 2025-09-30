package cluster

import (
	"context"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type disklessVolume struct {
	ctx      context.Context
	minorMgr MinorManager

	props  disklessVolumeProps
	dprops disklessVolumeDynamicProps
}

var _ volume = &disklessVolume{}

type disklessVolumeProps struct {
	nodeName string
	id       int
}

type disklessVolumeDynamicProps struct {
	minor uint
}

func (v *disklessVolume) initialize(existingRVRVolume *v1alpha2.Volume) error {
	if existingRVRVolume == nil {
		// minor
		minor, err := v.minorMgr.ReserveNodeMinor(v.ctx, v.props.nodeName)
		if err != nil {
			return err
		}
		v.dprops.minor = minor
	} else {
		// minor
		v.dprops.minor = existingRVRVolume.Device
	}

	// TODO: not handling existing LLVs for diskless replicas for now
	return nil
}

func (v *disklessVolume) reconcile() Action {
	// not creating llv for diskless replica
	return nil
}

func (v *disklessVolume) rvrVolume() v1alpha2.Volume {
	return v1alpha2.Volume{
		Number: uint(v.props.id),
		Device: v.dprops.minor,
	}
}

func (v *disklessVolume) shouldBeRecreated(rvrVol *v1alpha2.Volume) bool {
	if int(rvrVol.Number) != v.props.id {
		return true
	}
	if rvrVol.Disk != "" {
		return true
	}

	return false
}
