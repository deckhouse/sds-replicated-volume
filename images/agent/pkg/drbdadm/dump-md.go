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
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ExecuteDumpMD executes a command and returns:
// - (true, nil) if it exits with code 0
// - (false, nil) if it exits with code 1 and contains "No valid meta data found"
// - (false, error) for any other case
func ExecuteDumpMDMetadataExists(ctx context.Context, resource string) (bool, error) {
	cmd := exec.CommandContext(ctx, Command, DumpMDArgs(resource)...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		output := stderr.String()

		if exitCode == 1 && strings.Contains(output, "No valid meta data found") {
			return false, nil
		}
	}

	return false, errors.Join(err, errors.New(stderr.String()))
}
