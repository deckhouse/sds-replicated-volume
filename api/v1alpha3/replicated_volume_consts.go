/*
Copyright 2025 Flant JSC

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

package v1alpha3

// DRBD device minor number constants for ReplicatedVolume
const (
	// RVMinDeviceMinor is the minimum valid device minor number for DRBD devices in ReplicatedVolume
	RVMinDeviceMinor = uint(0)
	// RVMaxDeviceMinor is the maximum valid device minor number for DRBD devices in ReplicatedVolume
	// This value (1048575 = 2^20 - 1) corresponds to the maximum minor number
	// supported by modern Linux kernels (2.6+). DRBD devices are named as /dev/drbd<minor>,
	// and this range allows for up to 1,048,576 unique DRBD devices per major number.
	RVMaxDeviceMinor = uint(1048575)
)

// Shared secret hashing algorithms
const (
	// SharedSecretAlgSHA256 is the SHA256 hashing algorithm for shared secrets
	SharedSecretAlgSHA256 = "sha256"
	// SharedSecretAlgSHA1 is the SHA1 hashing algorithm for shared secrets
	SharedSecretAlgSHA1 = "sha1"
)

// SharedSecretAlgorithms returns the ordered list of supported shared secret algorithms.
// The order matters: algorithms are tried sequentially when one fails on any replica.
func SharedSecretAlgorithms() []string {
	return []string{
		SharedSecretAlgSHA256,
		SharedSecretAlgSHA1,
	}
}
