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

// Package blksize provides block device size queries using Linux ioctls.
//
// TODO: Refactor images/csi-driver/driver/node.go to use this package
// instead of its own inline BLKGETSIZE64 ioctl implementation.
package blksize

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// BLKGETSIZE64 is the Linux ioctl number for getting block device size in bytes.
const blkgetsize64 = 0x80081272

// GetDeviceSizeInSectors returns the block device size in 512-byte sectors.
func GetDeviceSizeInSectors(devicePath string) (uint64, error) {
	f, err := os.OpenFile(devicePath, os.O_RDONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("opening device %q: %w", devicePath, err)
	}
	defer f.Close()

	var sizeBytes uint64
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		blkgetsize64,
		uintptr(unsafe.Pointer(&sizeBytes)),
	)
	if errno != 0 {
		return 0, fmt.Errorf("ioctl BLKGETSIZE64 on %q: %w", devicePath, errno)
	}

	return sizeBytes / 512, nil
}
