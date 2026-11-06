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

// rsDiscardGranularityMax mirrors DRBD_RS_DISCARD_GRANULARITY_MAX from
// drbd_limits.h: the kernel never keeps an rs-discard-granularity above it.
const rsDiscardGranularityMax = 1 << 20

// ReadDiscardGranularity returns the rs-discard-granularity (in bytes) the
// DRBD kernel keeps for the block device at devPath when asked for the
// device's discard_granularity. devPath is typically /dev/<vg>/<lv> (a
// device-mapper symlink).
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
	granularity, ok := readQueueLimit(devName, "discard_granularity")
	if !ok {
		return 0
	}
	maxDiscardBytes, ok := readQueueLimit(devName, "discard_max_bytes")
	if !ok {
		return 0
	}

	return SanitizeRsDiscardGranularity(granularity, granularity, maxDiscardBytes)
}

// SanitizeRsDiscardGranularity returns the rs-discard-granularity the DRBD
// kernel stores when asked for value on a backing device with the given
// discard_granularity and discard_max_bytes, following sanitize_disk_conf()
// in drbd_nl.c. Asking for anything else makes the reconciler see a drift on
// every pass: a thin pool with 2 MiB chunks reports a discard_granularity of
// 2 MiB, which the kernel caps at 1 MiB.
func SanitizeRsDiscardGranularity(value, granularity, maxDiscardBytes uint64) uint {
	if value == 0 || granularity == 0 || maxDiscardBytes == 0 {
		return 0
	}

	sanitized := (value + granularity - 1) / granularity * granularity

	maxSectors := min(maxDiscardBytes>>9, rsDiscardGranularityMax>>9)
	sanitized = min(sanitized>>9, maxSectors) << 9

	if sanitized == 0 || (sanitized < granularity && granularity%sanitized != 0) {
		return 0
	}

	return uint(sanitized)
}

func readQueueLimit(devName, limit string) (uint64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/queue/%s", devName, limit))
	if err != nil {
		return 0, false
	}

	val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}

	return val, true
}
