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

package suite

import "k8s.io/apimachinery/pkg/api/resource"

// DRBD metadata layout constants.
// Copied from images/controller/internal/drbd_size/drbd_size.go.
const (
	drbdMaxPeers          = 7
	drbdSectorSize        = 512
	drbdBmSectPerBit      = 8
	drbdSuperblockSectors = 8
	drbdALSizeSectors     = 64
)

func drbdAlign(x, a uint64) uint64 {
	return (x + a - 1) &^ (a - 1)
}

func drbdBitmapSectors(capacitySectors uint64) uint64 {
	bits := drbdAlign(drbdAlign(capacitySectors, drbdBmSectPerBit)/drbdBmSectPerBit, 64)
	return drbdAlign(bits/8*drbdMaxPeers, 4096) / drbdSectorSize
}

func drbdMetadataSectors(lowerSectors uint64) uint64 {
	return drbdBitmapSectors(lowerSectors) + drbdSuperblockSectors + drbdALSizeSectors
}

// drbdUsableSize computes the usable DRBD upper-device size from a lower
// (backing) volume size, subtracting internal metadata overhead.
// Copied from images/controller/internal/drbd_size/drbd_size.go.
func drbdUsableSize(lowerSize resource.Quantity) resource.Quantity {
	lowerSectors := uint64(lowerSize.Value()) / drbdSectorSize
	usableSectors := lowerSectors - drbdMetadataSectors(lowerSectors)
	return *resource.NewQuantity(int64(usableSectors*drbdSectorSize), resource.BinarySI)
}
