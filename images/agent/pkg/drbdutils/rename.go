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
)

// Sentinel errors for rename operation.
var (
	ErrRenameUnknownResource = errors.New("unknown resource")
	ErrRenameAlreadyExists   = errors.New("already exists")
)

// RenameArgs returns the arguments for drbdsetup rename-resource command.
var RenameArgs = func(oldName, newName string) []string {
	return []string{"rename-resource", oldName, newName}
}

var RenameKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrRenameUnknownResource},
	{ExitCode: 10, OutputSubstring: "(174)", JoinErr: ErrRenameAlreadyExists},
}

// ExecuteRename renames a DRBD resource locally.
func ExecuteRename(ctx context.Context, oldName, newName string) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, RenameArgs(oldName, newName)...)
	_, err := executeCommand(cmd, RenameKnownErrors)
	return err
}
