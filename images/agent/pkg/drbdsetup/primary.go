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

// ExecutePrimary changes the role of a node in a resource to primary.
func ExecutePrimary(ctx context.Context, resource string, force bool) (err error) {
	args := PrimaryArgs(resource, force)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 17:
			err = ErrPrimaryNoUpToDateDisk
		case 158:
			err = ErrPrimaryResourceNotFound
		}
		return withOutput(err, out)
	}

	return nil
}
