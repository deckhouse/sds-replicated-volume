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
	"sync/atomic"
)

const MaxDeviceMinor = 1<<20 - 1 // 2^20 = 1048575

var (
	ErrNewMinorAlreadyExists    = errors.New("minor or volume already exists")
	ErrNewMinorResourceNotFound = errors.New("resource not found")
	ErrNewMinorNoFreeMinor      = errors.New("no free device minor available")
)

// drbdMajor is the block-device major number of DRBD devices. Entries in
// /sys/devices/virtual/bdi are named "<major>:<minor>", so it is what
// identifies the DRBD ones.
const drbdMajor = 147

// SysBlockPath is the path to /sys/block. Overridable in tests.
var SysBlockPath = "/sys/block"

// SysBDIPath is the path to /sys/devices/virtual/bdi. Overridable in tests.
var SysBDIPath = "/sys/devices/virtual/bdi"

// The auto-allocation counter is seeded once from the minors registered in
// sysfs and then handed out without touching sysfs again. Any new-minor
// failure clears nextDeviceMinorSeeded so the next allocation re-seeds from a
// fresh sysfs read: a failure means the assumption the counter rests on no
// longer holds.
var (
	nextDeviceMinorMu     sync.Mutex
	nextDeviceMinorSeeded atomic.Bool
	nextDeviceMinor       atomic.Uint64
)

// ResetNextDeviceMinor drops the seeded allocation state, so the next
// allocation re-reads sysfs. For testing only.
var ResetNextDeviceMinor = func() {
	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()
	nextDeviceMinor.Store(0)
	nextDeviceMinorSeeded.Store(false)
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
// A failure is never retried in place. drbdsetup rejects a minor that is
// already registered before it ever reaches device creation — in another
// resource or as another volume it fails with ERR_INVALID_REQUEST (162), and
// for the same resource and volume it succeeds without doing anything — so
// ERR_MINOR_OR_VOLUME_EXISTS (161) out of new-minor means the volume already
// exists in the resource, which no choice of minor can satisfy. Retrying it
// can only spin. Every error is therefore returned to the caller, which
// re-observes the on-node state and recomputes what to do; the counter is
// re-seeded from sysfs before the next allocation.
func ExecuteNewAutoMinor(ctx context.Context, resource string, volume uint, diskless bool) (uint, error) {
	if err := seedNextDeviceMinor(); err != nil {
		return 0, err
	}

	minor := uint(nextDeviceMinor.Add(1) - 1)
	if minor > MaxDeviceMinor {
		// Allocated past the end of the DRBD minor space. Re-seed so the next
		// attempt picks up minors freed below.
		nextDeviceMinorSeeded.Store(false)
		return 0, ErrNewMinorNoFreeMinor
	}

	if err := ExecuteNewMinor(ctx, resource, minor, volume, diskless); err != nil {
		nextDeviceMinorSeeded.Store(false)
		return 0, err
	}

	return minor, nil
}

// seedNextDeviceMinor seeds the allocation counter from the minors currently
// registered in sysfs, unless it is already seeded. The lock is released
// before returning so that a slow drbdsetup call in the caller never blocks
// concurrent allocations.
func seedNextDeviceMinor() error {
	if nextDeviceMinorSeeded.Load() {
		return nil
	}

	nextDeviceMinorMu.Lock()
	defer nextDeviceMinorMu.Unlock()

	if nextDeviceMinorSeeded.Load() {
		return nil
	}

	usedMinors, err := readUsedMinors()
	if err != nil {
		return fmt.Errorf("reading used minors from sysfs: %w", err)
	}

	nextDeviceMinor.Store(uint64(firstFreeMinor(usedMinors)))
	nextDeviceMinorSeeded.Store(true)
	return nil
}

// firstFreeMinor returns the minor to start allocating from: one past the
// highest minor in use, or — when that would run off the end of the DRBD minor
// space — the lowest unused minor. usedMinors must be sorted in ascending order.
// A return value above MaxDeviceMinor means every minor is in use.
func firstFreeMinor(usedMinors []uint) uint {
	if len(usedMinors) == 0 {
		return 0
	}

	next := usedMinors[len(usedMinors)-1] + 1
	if next <= MaxDeviceMinor {
		return next
	}

	// Wrapped past the maximum — scan from 0 to find the first gap.
	next = 0
	for _, m := range usedMinors {
		if m > next {
			break
		} else if m == next {
			next++
		}
	}
	return next
}

// readUsedMinors enumerates the DRBD minors that cannot be allocated, sorted
// ascending and deduplicated.
//
// Both sysfs directories that drbdsetup new-minor inspects are scanned, because
// it refuses a minor whose sysfs nodes still exist:
//
//   - /sys/block/drbd<minor>                — the block device
//   - /sys/devices/virtual/bdi/147:<minor>  — the backing-device info
//
// The bdi entry is reference-counted separately and outlives the block device
// for a short while after a device is torn down. Scanning only /sys/block would
// hand out a minor drbdsetup then rejects with (161), and since allocation
// re-seeds the same way after every failure, it would keep handing out that
// same minor until the leftover node is reaped.
func readUsedMinors() ([]uint, error) {
	minors, err := readMinorsFromDir(SysBlockPath, "drbd", false)
	if err != nil {
		return nil, err
	}

	bdiMinors, err := readMinorsFromDir(SysBDIPath, strconv.Itoa(drbdMajor)+":", true)
	if err != nil {
		return nil, err
	}

	minors = append(minors, bdiMinors...)
	slices.Sort(minors)
	return slices.Compact(minors), nil
}

// readMinorsFromDir returns the minor numbers of the entries of dir named
// prefix followed by a decimal number; entries that do not match are ignored.
// When optional is true, a missing directory yields no minors rather than an
// error.
func readMinorsFromDir(dir string, prefix string, optional bool) ([]uint, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var minors []uint
	for _, e := range entries {
		suffix, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(suffix, 10, 64)
		if err != nil {
			continue
		}
		minors = append(minors, uint(n))
	}

	return minors, nil
}
