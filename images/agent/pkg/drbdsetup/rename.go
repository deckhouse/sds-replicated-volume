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
	"strings"
)

// Sentinel errors for rename operation.
var (
	// ErrRenameUnknownResource indicates the source resource doesn't exist.
	ErrRenameUnknownResource = errors.New("unknown resource")
	// ErrRenameAlreadyExists indicates the target resource name already exists.
	ErrRenameAlreadyExists = errors.New("already exists")
)

// RenameArgs returns the arguments for drbdsetup rename-resource command.
var RenameArgs = func(oldName, newName string) []string {
	return []string{"rename-resource", oldName, newName}
}

// ExecuteRename renames a DRBD resource locally.
// This is a local operation only - it doesn't affect peer nodes.
// Returns ErrRenameUnknownResource if oldName doesn't exist.
// Returns ErrRenameAlreadyExists if newName already exists.
func ExecuteRename(ctx context.Context, oldName, newName string) (err error) {
	args := RenameArgs(oldName, newName)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil && !errors.Is(err, ErrRenameUnknownResource) && !errors.Is(err, ErrRenameAlreadyExists) {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		if errToExitCode(err) == 10 {
			outStr := string(out)
			if strings.Contains(outStr, "Unknown resource") {
				return ErrRenameUnknownResource
			}
			if strings.Contains(outStr, "Already exists") {
				return ErrRenameAlreadyExists
			}
		}
		return withOutput(err, out)
	}

	return nil
}
