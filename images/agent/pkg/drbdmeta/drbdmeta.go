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

package drbdmeta

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const Command = "drbdmeta"

var (
	ErrNoValidMetadata = errors.New("no valid metadata found")
)

// Cmd abstracts command execution for testing.
type Cmd interface {
	CombinedOutput() ([]byte, error)
}

// ExecCommandContextFactory creates a Cmd for the given command and arguments.
type ExecCommandContextFactory func(ctx context.Context, name string, arg ...string) Cmd

// ExecCommandContext is overridable for testing purposes.
var ExecCommandContext ExecCommandContextFactory = func(
	ctx context.Context,
	name string,
	arg ...string,
) Cmd {
	return exec.CommandContext(ctx, name, arg...)
}

// CreateMDArgs returns arguments for drbdmeta create-md command.
var CreateMDArgs = func(minor uint, backingDev string) []string {
	return []string{
		strconv.FormatUint(uint64(minor), 10),
		"v09",
		backingDev,
		"internal",
		"create-md",
		"--force",
		"31", // number-of-bitmap-slots (positional argument for v09 create-md)
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

// ExecuteCreateMD creates DRBD metadata on a backing device.
func ExecuteCreateMD(ctx context.Context, minor uint, backingDev string) error {
	args := CreateMDArgs(minor, backingDev)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}

// ExecuteCheckMD checks if DRBD metadata exists on a backing device.
// Returns (true, nil) if metadata exists, (false, nil) if not, or error on failure.
func ExecuteCheckMD(ctx context.Context, minor uint, backingDev string) (bool, error) {
	args := DumpMDArgs(minor, backingDev)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	output := string(out)
	if strings.Contains(output, "No valid meta data found") {
		return false, nil
	}

	return false, fmt.Errorf(
		"running command %s %v: %w; output: %q",
		Command, args, err, output,
	)
}
