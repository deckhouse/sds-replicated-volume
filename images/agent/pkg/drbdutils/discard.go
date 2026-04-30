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

package drbdutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadDiscardGranularity returns the discard_granularity (in bytes) for
// the block device at devPath by reading /sys/block/<dev>/queue/discard_granularity.
// devPath is typically /dev/<vg>/<lv> (a device-mapper symlink).
//
// Returns 0 when the device does not support discard, sysfs is unreadable,
// or the value cannot be parsed. Zero is the safe default: DRBD kernel
// sanitize_disk_conf() forces rs-discard-granularity to 0 on devices without
// discard support, so returning 0 avoids reconcile drift on thick LVM.
func ReadDiscardGranularity(devPath string) uint {
	realPath, err := filepath.EvalSymlinks(devPath)
	if err != nil {
		return 0
	}

	devName := filepath.Base(realPath)
	sysPath := fmt.Sprintf("/sys/block/%s/queue/discard_granularity", devName)

	data, err := os.ReadFile(sysPath)
	if err != nil {
		return 0
	}

	val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}

	return uint(val)
}
