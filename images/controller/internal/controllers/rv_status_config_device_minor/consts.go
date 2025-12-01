package rvstatusconfigdeviceminor

const (
	// MaxDeviceMinor is the maximum valid device minor number for DRBD devices.
	// This value (1048575 = 2^20 - 1) corresponds to the maximum minor number
	// supported by modern Linux kernels (2.6+). DRBD devices are named as /dev/drbd<minor>,
	// and this range allows for up to 1,048,576 unique DRBD devices per major number.
	// This matches the validation in api/v1alpha3/replicated_volume.go (Maximum=1048575).
	MaxDeviceMinor = 1048575
	// MinDeviceMinor is the minimum valid device minor number (0).
	// Minor number 0 is valid and will create /dev/drbd0 device.
	MinDeviceMinor = 0
)
