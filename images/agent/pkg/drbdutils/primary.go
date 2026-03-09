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

var (
	ErrPrimaryNoUpToDateDisk   = errors.New("no up-to-date disk available")
	ErrPrimaryResourceNotFound = errors.New("resource not found")
)

// PrimaryArgs returns the arguments for drbdsetup primary command.
var PrimaryArgs = func(resource string, force bool) []string {
	args := []string{"primary", resource}
	if force {
		args = append(args, "--force")
	}
	return args
}

var PrimaryKnownErrors = []KnownError{
	{ExitCode: 17, OutputSubstring: "(-2)", JoinErr: ErrPrimaryNoUpToDateDisk},
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrPrimaryResourceNotFound},
}

// ExecutePrimary changes the role of a node in a resource to primary.
func ExecutePrimary(ctx context.Context, resource string, force bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, PrimaryArgs(resource, force)...)
	_, err := executeCommand(cmd, PrimaryKnownErrors)
	return err
}
