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
	"strconv"
	"strings"
)

var (
	ErrNoValidMetadata = errors.New("no valid metadata found")
	ErrUncleanMetadata = errors.New("metadata is unclean (activity log not applied)")
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

// DumpMDKnownErrors matches exit 1 stderr: "No valid meta data found", "unclean", "operation failed".
var DumpMDKnownErrors = []KnownError{
	{ExitCode: 1, OutputSubstring: "No valid meta data found", JoinErr: ErrNoValidMetadata},
	{ExitCode: 1, OutputSubstring: "unclean", JoinErr: ErrUncleanMetadata},
}

// ExecuteCreateMD creates DRBD metadata on a backing device.
func ExecuteCreateMD(ctx context.Context, minor uint, backingDev string) error {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, CreateMDArgs(minor, backingDev)...)
	_, err := executeCommand(cmd, nil)
	return err
}

// ExecuteCheckMD checks if DRBD metadata exists on a backing device.
// Returns (true, nil) if metadata exists, (false, nil) if not, or error on failure.
// Metadata with a dirty activity log ("unclean") is still counted as existing.
func ExecuteCheckMD(ctx context.Context, minor uint, backingDev string) (bool, error) {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, DumpMDArgs(minor, backingDev)...)
	_, err := executeCommand(cmd, DumpMDKnownErrors)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNoValidMetadata) {
		return false, nil
	}
	if errors.Is(err, ErrUncleanMetadata) {
		return true, nil
	}
	return false, err
}

// ApplyALArgs returns arguments for drbdmeta apply-al command (replay activity log, mark metadata clean).
var ApplyALArgs = func(minor uint, backingDev string) []string {
	return []string{
		"-f",
		strconv.FormatUint(uint64(minor), 10),
		"v09",
		backingDev,
		"internal",
		"apply-al",
	}
}

// ExecuteApplyAL replays the DRBD activity log on a backing device.
// Required before attach when metadata is unclean (e.g. after drbdsetup down without clean detach).
func ExecuteApplyAL(ctx context.Context, minor uint, backingDev string) error {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, ApplyALArgs(minor, backingDev)...)
	_, err := executeCommand(cmd, nil)
	return err
}

// ReadDevUUIDArgs returns arguments for drbdmeta read-dev-uuid.
// Uses "-" as the minor to skip per-minor locking (read-dev-uuid is read-only,
// modifies_md=0, so no real minor is needed). The --force flag auto-confirms
// the O_EXCL retry prompt when the backing device is held by the DRBD kernel.
// Output: exactly 16 hex digits, e.g. "00A3F7E21BC04D59".
var ReadDevUUIDArgs = func(backingDev string) []string {
	return []string{
		"--force",
		"-",
		"v09",
		backingDev,
		"internal",
		"read-dev-uuid",
	}
}

// WriteDevUUIDArgs returns arguments for drbdmeta write-dev-uuid (device must not be attached).
var WriteDevUUIDArgs = func(minor uint, backingDev string, uuid string) []string {
	return []string{
		"-f",
		strconv.FormatUint(uint64(minor), 10),
		"v09",
		backingDev,
		"internal",
		"write-dev-uuid",
		uuid,
	}
}

// ExecuteReadDevUUID reads the device-uuid from DRBD metadata on a backing device.
// Returns the 16-digit uppercase hex string as printed by drbdmeta, or empty if all zeroes.
// Safe to call on attached devices (read-only, uses "-" minor).
//
// drbdmeta may write warnings to stderr (e.g. stale-data on attached devices).
// Since executeCommand uses CombinedOutput, the UUID is extracted by scanning
// for a line that matches the expected 16-hex-digit format.
func ExecuteReadDevUUID(ctx context.Context, backingDev string) (string, error) {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, ReadDevUUIDArgs(backingDev)...)
	out, err := executeCommand(cmd, nil)
	if err != nil {
		return "", err
	}
	uuid := parseDevUUID(string(out))
	if uuid == "" {
		return "", fmt.Errorf("no device-uuid found in drbdmeta output: %q", string(out))
	}
	if uuid == "0000000000000000" {
		return "", nil
	}
	return uuid, nil
}

func parseDevUUID(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 16 && isUpperHex(line) {
			return line
		}
	}
	return ""
}

func isUpperHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// ExecuteWriteDevUUID writes a device-uuid into DRBD metadata on a backing device.
func ExecuteWriteDevUUID(ctx context.Context, minor uint, backingDev string, uuid string) error {
	cmd := ExecCommandContext(ctx, DRBDMetaCommand, WriteDevUUIDArgs(minor, backingDev, uuid)...)
	_, err := executeCommand(cmd, nil)
	return err
}
