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

package drbdsetup

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

// ExecuteNewMinor creates a new DRBD device/volume within a resource.
// When diskless is true, --diskless is passed to mark the device as an intentionally diskless client.
func ExecuteNewMinor(ctx context.Context, resource string, minor uint, volume uint, diskless bool) (err error) {
	args := NewMinorArgs(resource, minor, volume, diskless)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 161:
			err = ErrNewMinorAlreadyExists
		case 158:
			err = ErrNewMinorResourceNotFound
		}
		return withOutput(err, out)
	}

	return nil
}

// ExecuteNewAutoMinor creates a new DRBD device/volume with auto-allocated minor.
// When diskless is true, --diskless is passed to mark the device as an intentionally diskless client.
// Returns the allocated minor number on success.
func ExecuteNewAutoMinor(ctx context.Context, resource string, volume uint, diskless bool) (uint, error) {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()

	for {
		minor := nextDeviceMinor
		err := ExecuteNewMinor(ctx, resource, nextDeviceMinor, volume, diskless)
		if err == nil {
			_ = incrementDeviceMinor(nil) // error can only occur with exhausted minors; nil input skips that check
			return minor, nil
		}

		if !errors.Is(err, ErrNewMinorAlreadyExists) {
			return 0, err
		}

		resources, err := ExecuteShow(ctx, "", false)
		if err != nil {
			return 0, fmt.Errorf("querying show to find used minors: %w", err)
		}

		if err := incrementDeviceMinor(resources); err != nil {
			return 0, err
		}
	}
}

func incrementDeviceMinor(resources []ShowResource) error {
	var totalUsedMinors int
	for _, res := range resources {
		for _, vol := range res.ThisHost.Volumes {
			totalUsedMinors++
			nextDeviceMinor = max(nextDeviceMinor, uint(vol.DeviceMinor))
		}
	}

	if totalUsedMinors > MaxDeviceMinor {
		return ErrNewMinorNoFreeMinor
	}

	nextDeviceMinor++

	if nextDeviceMinor > MaxDeviceMinor {
		nextDeviceMinor = 0

		// find the minimal hole
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
