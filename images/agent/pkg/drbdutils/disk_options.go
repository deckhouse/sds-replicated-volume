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
	"strconv"
)

var (
	ErrDiskOptionsNoDiskAttached = errors.New("no disk attached")
)

// DiskOptions contains options for drbdsetup disk-options command.
type DiskOptions struct {
	DiscardZeroesIfAligned *bool
	RsDiscardGranularity   *uint
}

// DiskOptionsArgs returns arguments for drbdsetup disk-options command.
var DiskOptionsArgs = func(minor uint, opts DiskOptions) []string {
	args := []string{
		"disk-options",
		strconv.FormatUint(uint64(minor), 10),
	}

	if opts.DiscardZeroesIfAligned != nil {
		if *opts.DiscardZeroesIfAligned {
			args = append(args, "--discard-zeroes-if-aligned=yes")
		} else {
			args = append(args, "--discard-zeroes-if-aligned=no")
		}
	}

	if opts.RsDiscardGranularity != nil {
		args = append(args, "--rs-discard-granularity", strconv.FormatUint(uint64(*opts.RsDiscardGranularity), 10))
	}

	return args
}

var DiskOptionsKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(138)", JoinErr: ErrDiskOptionsNoDiskAttached},
}

// ExecuteDiskOptions changes disk options on an attached device.
func ExecuteDiskOptions(ctx context.Context, minor uint, opts DiskOptions) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, DiskOptionsArgs(minor, opts)...)
	_, err := executeCommand(cmd, DiskOptionsKnownErrors)
	return err
}
