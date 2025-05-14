package v9

// Define a volume within a resource. The volume numbers in the various [Volume]
// sections of a resource define which devices on which hosts form a replicated
// device.
type Volume struct {
	Number int
	// Define the device name and minor number of a replicated block device. This is the device that applications are supposed to access; in most cases, the device is not used directly, but as a file system. This parameter is required and the standard device naming convention is assumed.
	//
	// In addition to this device, udev will create /dev/drbd/by-res/resource/volume and /dev/drbd/by-disk/lower-level-device symlinks to the device.
	DeviceMinorNumber uint32

	// Define the lower-level block device that DRBD will use for storing the actual data. While the replicated drbd device is configured, the lower-level device must not be used directly. Even read-only access with tools like dumpe2fs(8) and similar is not allowed. The keyword none specifies that no lower-level block device is configured; this also overrides inheritance of the lower-level device.
	//
	// Either [VolumeDisk] or [VolumeDiskNone].
	Disk DiskValue

	DiskOptions *DiskOptions

	// Define where the metadata of a replicated block device resides: it can be internal, meaning that the lower-level device contains both the data and the metadata, or on a separate device.
	//
	// When the index form of this parameter is used, multiple replicated devices can share the same metadata device, each using a separate index. Each index occupies 128 MiB of data, which corresponds to a replicated device size of at most 4 TiB with two cluster nodes. We recommend not to share metadata devices anymore, and to instead use the lvm volume manager for creating metadata devices as needed.
	//
	// When the index form of this parameter is not used, the size of the lower-level device determines the size of the metadata. The size needed is 36 KiB + (size of lower-level device) / 32K * (number of nodes - 1). If the metadata device is bigger than that, the extra space is not used.
	//
	// This parameter is required if a disk other than none is specified, and ignored if disk is set to none. A meta-disk parameter without a disk parameter is not allowed.
	//
	// Either [VolumeMetaDiskInternal] or [VolumeMetaDiskDevice].
	MetaDisk MetaDiskValue
}

type DiskValue interface {
	_diskValue()
}

type VolumeDiskNone struct{}

var _ DiskValue = new(VolumeDiskNone)

func (v *VolumeDiskNone) _diskValue() {}

type VolumeDisk string

var _ DiskValue = new(VolumeDisk)

func (v *VolumeDisk) _diskValue() {}

type MetaDiskValue interface {
	_metaDiskValue()
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
