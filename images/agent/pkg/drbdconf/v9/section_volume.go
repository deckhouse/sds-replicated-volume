package v9

import (
	"errors"
	"strconv"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

// Define a volume within a resource. The volume numbers in the various [Volume]
// sections of a resource define which devices on which hosts form a replicated
// device.
type Volume struct {
	Number *int `drbd:""`

	DiskOptions *DiskOptions

	// Define the device name and minor number of a replicated block device.
	// This is the device that applications are supposed to access; in most
	// cases, the device is not used directly, but as a file system. This
	// parameter is required and the standard device naming convention is
	// assumed.
	//
	// In addition to this device, udev will create
	// /dev/drbd/by-res/resource/volume and /dev/drbd/by-disk/lower-level-device
	// symlinks to the device.
	Device *DeviceMinorNumber `drbd:"device"`

	// Define the lower-level block device that DRBD will use for storing the
	// actual data. While the replicated drbd device is configured, the
	// lower-level device must not be used directly. Even read-only access with
	// tools like dumpe2fs(8) and similar is not allowed. The keyword none
	// specifies that no lower-level block device is configured; this also
	// overrides inheritance of the lower-level device.
	//
	// Either [VolumeDisk] or [VolumeDiskNone].
	Disk DiskValue `drbd:"disk"`

	// Define where the metadata of a replicated block device resides: it can be
	// internal, meaning that the lower-level device contains both the data and
	// the metadata, or on a separate device.
	//
	// When the index form of this parameter is used, multiple replicated
	// devices can share the same metadata device, each using a separate index.
	// Each index occupies 128 MiB of data, which corresponds to a replicated
	// device size of at most 4 TiB with two cluster nodes. We recommend not to
	// share metadata devices anymore, and to instead use the lvm volume manager
	// for creating metadata devices as needed.
	//
	// When the index form of this parameter is not used, the size of the
	// lower-level device determines the size of the metadata. The size needed
	// is 36 KiB + (size of lower-level device) / 32K * (number of nodes - 1).
	// If the metadata device is bigger than that, the extra space is not used.
	//
	// This parameter is required if a disk other than none is specified, and
	// ignored if disk is set to none. A meta-disk parameter without a disk
	// parameter is not allowed.
	//
	// Either [VolumeMetaDiskInternal] or [VolumeMetaDiskDevice].
	MetaDisk MetaDiskValue `drbd:"meta-disk"`
}

var _ drbdconf.SectionKeyworder = &Volume{}

func (v *Volume) SectionKeyword() string {
	return "volume"
}

//

type DeviceMinorNumber uint

func (d *DeviceMinorNumber) MarshalParameter() ([]string, error) {
	return []string{"/dev/drbd" + strconv.FormatUint(uint64(*d), 10)}, nil
}

func (d *DeviceMinorNumber) UnmarshalParameter(p []drbdconf.Word) error {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return err
	}

	var numberStr string
	if after, found := strings.CutPrefix(p[1].Value, "/dev/drbd"); found {
		numberStr = after
	} else if p[1].Value == "minor" {
		// also try one old format:
		// "device minor <nr>"
		if err := drbdconf.EnsureLen(p, 3); err != nil {
			return err
		}
		numberStr = p[2].Value
	} else {
		return errors.New("unrecognized value format")
	}

	n, err := strconv.ParseUint(numberStr, 10, 64)
	if err != nil {
		return err
	}
	*d = DeviceMinorNumber(n)

	return nil
}

var _ drbdconf.ParameterCodec = new(DeviceMinorNumber)

//

type DiskValue interface {
	_diskValue()
}

func init() {
	drbdconf.RegisterParameterTypeCodec[DiskValue](
		&DiskValueParameterTypeCodec{},
	)
}

type DiskValueParameterTypeCodec struct {
}

func (d *DiskValueParameterTypeCodec) MarshalParameter(
	v any,
) ([]string, error) {
	switch typedVal := v.(type) {
	case *VolumeDiskNone:
		return []string{"none"}, nil
	case *VolumeDisk:
		return []string{string(*typedVal)}, nil
	}
	return nil, errors.New("unexpected DiskValue value")
}

func (d *DiskValueParameterTypeCodec) UnmarshalParameter(
	p []drbdconf.Word,
) (any, error) {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return nil, err
	}
	if p[1].Value == "none" {
		return &VolumeDiskNone{}, nil
	}
	return ptr(VolumeDisk(p[1].Value)), nil
}

type VolumeDiskNone struct{}

var _ DiskValue = &VolumeDiskNone{}

func (v *VolumeDiskNone) _diskValue() {}

type VolumeDisk string

var _ DiskValue = new(VolumeDisk)

func (v *VolumeDisk) _diskValue() {}

//

type MetaDiskValue interface {
	_metaDiskValue()
}

func init() {
	drbdconf.RegisterParameterTypeCodec[MetaDiskValue](
		&MetaDiskValueParameterTypeCodec{},
	)
}

type MetaDiskValueParameterTypeCodec struct {
}

func (d *MetaDiskValueParameterTypeCodec) MarshalParameter(
	v any,
) ([]string, error) {
	switch typedVal := v.(type) {
	case *VolumeMetaDiskInternal:
		return []string{"internal"}, nil
	case *VolumeMetaDiskDevice:
		res := []string{typedVal.Device}
		if typedVal.Index != nil {
			res = append(res, strconv.FormatUint(uint64(*typedVal.Index), 10))
		}
		return res, nil
	}
	return nil, errors.New("unexpected MetaDiskValue value")
}

func (d *MetaDiskValueParameterTypeCodec) UnmarshalParameter(
	p []drbdconf.Word,
) (any, error) {
	if err := drbdconf.EnsureLen(p, 2); err != nil {
		return nil, err
	}
	if p[1].Value == "internal" {
		return &VolumeMetaDiskInternal{}, nil
	}

	res := &VolumeMetaDiskDevice{
		Device: p[1].Value,
	}

	if len(p) >= 3 {
		idx, err := strconv.ParseUint(p[2].Value, 10, 64)
		if err != nil {
			return nil, err
		}
		res.Index = ptr(uint(idx))
	}

	return res, nil
}

type VolumeMetaDiskInternal struct{}

var _ MetaDiskValue = new(VolumeMetaDiskInternal)

func (v *VolumeMetaDiskInternal) _metaDiskValue() {}

type VolumeMetaDiskDevice struct {
	Device string
	Index  *uint
}

var _ MetaDiskValue = new(VolumeMetaDiskDevice)

func (v *VolumeMetaDiskDevice) _metaDiskValue() {}
