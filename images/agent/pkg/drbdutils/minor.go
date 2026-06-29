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
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const MaxDeviceMinor = 1<<20 - 1 // 2^20 = 1048575

var (
	ErrNewMinorAlreadyExists    = errors.New("minor or volume already exists")
	ErrNewMinorResourceNotFound = errors.New("resource not found")
	ErrNewMinorNoFreeMinor      = errors.New("no free device minor available")
)

// SysBlockPath is the path to /sys/block. Overridable in tests.
var SysBlockPath = "/sys/block"

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

		usedMinors, err := readUsedMinors()
		if err != nil {
			return 0, fmt.Errorf("reading used minors from sysfs: %w", err)
		}

		if err := advanceMinorPastUsed(usedMinors); err != nil {
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

// readUsedMinors enumerates active DRBD minors by scanning /sys/block/drbd*.
// Returns a sorted slice of minor numbers.
func readUsedMinors() ([]uint, error) {
	entries, err := os.ReadDir(SysBlockPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", SysBlockPath, err)
	}

	var minors []uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "drbd") {
			continue
		}
		n, err := strconv.ParseUint(name[len("drbd"):], 10, 64)
		if err != nil {
			continue
		}
		minors = append(minors, uint(n))
	}

	slices.Sort(minors)
	return minors, nil
}

// advanceMinorPastUsed advances nextDeviceMinor past all used minors.
// usedMinors must be sorted in ascending order. Called on minor collision.
func advanceMinorPastUsed(usedMinors []uint) error {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()

	if len(usedMinors) > MaxDeviceMinor {
		return ErrNewMinorNoFreeMinor
	}

	if len(usedMinors) > 0 {
		nextDeviceMinor = max(nextDeviceMinor, usedMinors[len(usedMinors)-1]+1)
	}

	if nextDeviceMinor > MaxDeviceMinor {
		// Wrapped past the maximum — scan from 0 to find the first gap.
		nextDeviceMinor = 0

		for _, m := range usedMinors {
			if m > nextDeviceMinor {
				return nil
			} else if m == nextDeviceMinor {
				nextDeviceMinor++
			}
		}
	}

	return nil
}
