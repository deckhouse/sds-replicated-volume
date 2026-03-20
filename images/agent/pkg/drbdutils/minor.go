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
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
)

const MaxDeviceMinor = 1<<20 - 1 // 2^20 = 1048575

var (
	ErrNewMinorAlreadyExists    = errors.New("minor or volume already exists")
	ErrNewMinorResourceNotFound = errors.New("resource not found")
	ErrNewMinorNoFreeMinor      = errors.New("no free device minor available")
)

var (
	nextDeviceMinor   uint
	nextDeviceMinorMu sync.Mutex
)

// ResetNextDeviceMinor resets the auto-allocation counter. For testing only.
var ResetNextDeviceMinor = func() {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()
	nextDeviceMinor = 0
}

// NewMinorArgs returns the arguments for drbdsetup new-minor command.
// When diskless is true, --diskless is appended to mark the device as an intentionally diskless client.
var NewMinorArgs = func(resource string, minor uint, volume uint, diskless bool) []string {
	args := []string{
		"new-minor", resource,
		strconv.FormatUint(uint64(minor), 10),
		strconv.FormatUint(uint64(volume), 10),
	}
	if diskless {
		args = append(args, "--diskless")
	}
	return args
}

var NewMinorKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(161)", JoinErr: ErrNewMinorAlreadyExists},
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrNewMinorResourceNotFound},
}

// ExecuteNewMinor creates a new DRBD device/volume within a resource.
// When diskless is true, --diskless is passed to mark the device as an intentionally diskless client.
func ExecuteNewMinor(ctx context.Context, resource string, minor uint, volume uint, diskless bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, NewMinorArgs(resource, minor, volume, diskless)...)
	_, err := executeCommand(cmd, NewMinorKnownErrors)
	return err
}

// ExecuteNewAutoMinor creates a new DRBD device/volume with auto-allocated minor.
// When diskless is true, --diskless is passed to mark the device as an intentionally diskless client.
// Returns the allocated minor number on success.
//
// The mutex is held only for counter operations (microseconds), not during exec.
// Kernel safety: DRBD parallel_ops=true and per-resource conf_update mutex
// guarantee that concurrent drbdsetup new-minor for different resources is safe.
// Minor number collisions are caught by the kernel (-EEXIST) and handled via retry.
func ExecuteNewAutoMinor(ctx context.Context, resource string, volume uint, diskless bool) (uint, error) {
	for {
		minor := grabNextMinor()

		err := ExecuteNewMinor(ctx, resource, minor, volume, diskless)
		if err == nil {
			return minor, nil
		}

		if !errors.Is(err, ErrNewMinorAlreadyExists) {
			return 0, err
		}

		resources, err := ExecuteShow(ctx, "", false)
		if err != nil {
			return 0, fmt.Errorf("querying show to find used minors: %w", err)
		}

		if err := advanceMinorPastUsed(resources); err != nil {
			return 0, err
		}
	}
}

// grabNextMinor atomically claims the next candidate minor number.
// Each concurrent caller gets a unique minor, preventing contention on exec.
func grabNextMinor() uint {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()
	minor := nextDeviceMinor
	nextDeviceMinor++
	if nextDeviceMinor > MaxDeviceMinor {
		nextDeviceMinor = 0
	}
	return minor
}

// advanceMinorPastUsed scans used minors from drbdsetup show output and
// advances nextDeviceMinor past all of them. Called on minor collision.
func advanceMinorPastUsed(resources []ShowResource) error {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()

	var totalUsedMinors int
	for _, res := range resources {
		for _, vol := range res.ThisHost.Volumes {
			totalUsedMinors++
			nextDeviceMinor = max(nextDeviceMinor, uint(vol.DeviceMinor)+1)
		}
	}

	if totalUsedMinors > MaxDeviceMinor {
		return ErrNewMinorNoFreeMinor
	}

	if nextDeviceMinor > MaxDeviceMinor {
		nextDeviceMinor = 0

		if len(resources) > 0 {
			usedMinors := make([]uint, 0, totalUsedMinors)
			for _, res := range resources {
				for _, vol := range res.ThisHost.Volumes {
					usedMinors = append(usedMinors, uint(vol.DeviceMinor))
				}
			}
			slices.Sort(usedMinors)
			for _, m := range usedMinors {
				if m > nextDeviceMinor {
					return nil
				} else if m == nextDeviceMinor {
					nextDeviceMinor++
				}
			}
		}
	}

	return nil
}
