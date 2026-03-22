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

// Package drbd_size provides DRBD metadata size calculations.
package drbd_size

import "k8s.io/apimachinery/pkg/api/resource"

const (
	maxPeers          = 7   // DRBD maximum peers
	sectorSize        = 512 // bytes per sector
	bmSectPerBit      = 8   // sectors per bitmap bit (1 << (12 - 9))
	superblockSectors = 8   // 4096 bytes / 512
	alSizeSectors     = 64  // 1 stripe * 32KB / 512
)

// align rounds up x to the nearest multiple of a.
func align(x, a uint64) uint64 {
	return (x + a - 1) &^ (a - 1)
}

// bitmapSectors calculates on-disk bitmap size in sectors for given capacity.
func bitmapSectors(capacitySectors uint64) uint64 {
	bits := align(align(capacitySectors, bmSectPerBit)/bmSectPerBit, 64)
	return align(bits/8*maxPeers, 4096) / sectorSize
}

// metadataSectors returns total DRBD metadata overhead in sectors.
func metadataSectors(lowerSectors uint64) uint64 {
	return bitmapSectors(lowerSectors) + superblockSectors + alSizeSectors
}

// UsableSize calculates the usable capacity available on a DRBD lower volume
// of the given size, after subtracting internal metadata overhead
// (bitmap, activity log, superblock).
//
// Precondition: lowerSize must be positive (enforced by Kubernetes API validation).
func UsableSize(lowerSize resource.Quantity) resource.Quantity {
	lowerSectors := uint64(lowerSize.Value()) / sectorSize
	usableSectors := lowerSectors - metadataSectors(lowerSectors)
	return *resource.NewQuantity(int64(usableSectors*sectorSize), resource.BinarySI)
}

// LowerVolumeSize calculates the required lower (backing) volume size for DRBD
// given the desired usable capacity. Accounts for internal metadata overhead
// (bitmap, activity log, superblock).
//
// Precondition: usableSize must be positive (enforced by Kubernetes API validation).
func LowerVolumeSize(usableSize resource.Quantity) resource.Quantity {
	usableSectors := uint64(usableSize.Value()) / sectorSize
	lowerSectors := usableSectors

	// Fixed-point iteration: find smallest lowerSectors where
	// lowerSectors >= align(usableSectors + metadata(lowerSectors), 8)
	//
	// Convergence is guaranteed because metadata grows sublinearly with capacity:
	// bitmap size is O(capacity / 4KB), so each iteration adds less overhead than
	// the previous one. In practice, this converges in 2-3 iterations.
	// The loop limit of 10 is a defensive safeguard that should never be reached.
	for i := 0; i < 10; i++ {
		required := align(usableSectors+metadataSectors(lowerSectors), 8)
		if required <= lowerSectors {
			return *resource.NewQuantity(int64(required*sectorSize), resource.BinarySI)
		}
		lowerSectors = required
	}

	// Unreachable due to guaranteed convergence (sublinear metadata growth).
	panic("drbd_size: LowerVolumeSize failed to converge")
}
