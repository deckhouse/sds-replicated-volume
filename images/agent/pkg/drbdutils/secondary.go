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
	ErrSecondaryDeviceInUse      = errors.New("device in use, cannot demote")
	ErrSecondaryResourceNotFound = errors.New("resource not found")
)

// SecondaryArgs returns the arguments for drbdsetup secondary command.
var SecondaryArgs = func(resource string, force bool) []string {
	args := []string{"secondary", resource}
	if force {
		args = append(args, "--force")
	}
	return args
}

var SecondaryKnownErrors = []KnownError{
	{ExitCode: 11, OutputSubstring: "(-12)", JoinErr: ErrSecondaryDeviceInUse},
	{ExitCode: 10, OutputSubstring: "(158)", JoinErr: ErrSecondaryResourceNotFound},
}

// ExecuteSecondary changes the role of a node in a resource to secondary.
func ExecuteSecondary(ctx context.Context, resource string, force bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, SecondaryArgs(resource, force)...)
	_, err := executeCommand(cmd, SecondaryKnownErrors)
	return err
}
