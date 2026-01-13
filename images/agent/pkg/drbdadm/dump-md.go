/*
Copyright 2025 Flant JSC

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

package drbdadm

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Metadata contains parsed DRBD metadata fields.
type Metadata struct {
	DeviceUUID string
	NodeID     int
}

// ExecuteDumpMDParsed runs drbdadm dump-md and parses output.
// Returns:
// - (*Metadata, true, nil) if metadata exists and was parsed successfully
// - (nil, false, nil) if it exits with code 1 and contains "No valid meta data found"
// - (nil, false/true, error) for any other case (command error or parse error)
// Should only be called when device is DOWN.
func ExecuteDumpMDParsed(ctx context.Context, resource string) (*Metadata, bool, CommandError) {
	args := DumpMDArgs(resource)
	cmd := ExecCommandContext(ctx, Command, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		exitCode := errToExitCode(err)
		outputStr := string(output)

		if exitCode == 1 && strings.Contains(outputStr, "No valid meta data found") {
			return nil, false, nil // metadata doesn't exist
		}

		return nil, false, &commandError{
			error:           err,
			commandWithArgs: append([]string{Command}, args...),
			output:          outputStr,
			exitCode:        exitCode,
		}
	}

	md, parseErr := parseMetadata(string(output))
	if parseErr != nil {
		return nil, true, &commandError{
			error:           parseErr,
			commandWithArgs: append([]string{Command}, args...),
			output:          string(output),
			exitCode:        0,
		}
	}

	return md, true, nil
}

var (
	deviceUUIDRegex = regexp.MustCompile(`device-uuid\s+(0x[0-9A-Fa-f]+)`)
	// node-id can be -1 for freshly created metadata (before first "up")
	nodeIDRegex = regexp.MustCompile(`node-id\s+(-?\d+)`)
)

// NodeIDUninitialized indicates metadata exists but resource was never up'd.
// DRBD writes node-id from config to metadata only during first "up".
const NodeIDUninitialized = -1

func parseMetadata(output string) (*Metadata, error) {
	md := &Metadata{
		NodeID: NodeIDUninitialized, // default if not found
	}

	// Parse: device-uuid 0xABCDEF123456789A;
	if match := deviceUUIDRegex.FindStringSubmatch(output); len(match) > 1 {
		md.DeviceUUID = match[1]
	}

	// Parse: node-id 0; (can be -1 for uninitialized metadata)
	if match := nodeIDRegex.FindStringSubmatch(output); len(match) > 1 {
		var err error
		md.NodeID, err = strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("parsing node-id: %w", err)
		}
	}

	if md.DeviceUUID == "" {
		return nil, fmt.Errorf("device-uuid not found in metadata output")
	}

	return md, nil
}
