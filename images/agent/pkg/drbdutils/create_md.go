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
	ErrNoValidMetadata = errors.New("no valid metadata found")
)

// CreateMDArgs returns arguments for drbdmeta create-md command.
var CreateMDArgs = func(minor uint, backingDev string) []string {
	return []string{
		strconv.FormatUint(uint64(minor), 10),
		"v09",
		backingDev,
		"internal",
		"create-md",
		"--force",
		// number-of-bitmap-slots (positional argument for v09 create-md)
		// Must match maxPeers in images/controller/internal/drbd_size/drbd_size.go
		// and --max-peers in images/agent/pkg/drbdadm/vars.go.
		"7",
	}
}

// DumpMDArgs returns arguments for drbdmeta dump-md command.
var DumpMDArgs = func(minor uint, backingDev string) []string {
	return []string{
		strconv.FormatUint(uint64(minor), 10),
		"v09",
		backingDev,
		"internal",
		"dump-md",
	}
}

var DumpMDKnownErrors = []KnownError{
	{ExitCode: 1, OutputSubstring: "No valid meta data found", JoinErr: ErrNoValidMetadata},
}

// ExecuteCreateMD creates DRBD metadata on a backing device.
func ExecuteCreateMD(ctx context.Context, minor uint, backingDev string) error {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, CreateMDArgs(minor, backingDev)...)
	_, err := executeCommand(cmd, nil)
	return err
}

// ExecuteCheckMD checks if DRBD metadata exists on a backing device.
// Returns (true, nil) if metadata exists, (false, nil) if not, or error on failure.
func ExecuteCheckMD(ctx context.Context, minor uint, backingDev string) (bool, error) {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, DumpMDArgs(minor, backingDev)...)
	_, err := executeCommand(cmd, DumpMDKnownErrors)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNoValidMetadata) {
		return false, nil
	}
	return false, err
}
