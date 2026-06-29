/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package drbdm

const (
	// ControllerName is the stable name for the DRBD mapper controller.
	ControllerName = "drbdm-controller"

	// ScannerName is the name of the DRBD mapper scanner component.
	ScannerName = "drbdm-scanner"

	// deviceMapperPrefix is the path prefix for device-mapper devices.
	deviceMapperPrefix = "/dev/mapper/"

	// internalDeviceSuffix is appended to the CR name to form the internal dm-linear device name.
	internalDeviceSuffix = "-internal"
)

// UpperDevicePath returns the user-facing upper device path for a DRBDMapper resource.
func UpperDevicePath(name string) string {
	return deviceMapperPrefix + name
}

// InternalDeviceName returns the internal dm-linear device name.
func InternalDeviceName(name string) string {
	return name + internalDeviceSuffix
}

// InternalDevicePath returns the full path of the internal dm-linear device.
func InternalDevicePath(name string) string {
	return deviceMapperPrefix + InternalDeviceName(name)
}
