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

package drbdsetup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

var (
	ErrNewResourceAlreadyExists    = errors.New("resource already exists")
	ErrNewResourcePermissionDenied = errors.New("permission denied")
)

// NewResourceArgs returns the arguments for drbdsetup new-resource command.
var NewResourceArgs = func(resource string, nodeID uint8) []string {
	return []string{"new-resource", resource, strconv.FormatUint(uint64(nodeID), 10)}
}

// ExecuteNewResource creates a new DRBD resource.
func ExecuteNewResource(ctx context.Context, resource string, nodeID uint8) error {
	args := NewResourceArgs(resource, nodeID)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 161:
			return ErrNewResourceAlreadyExists
		case 152:
			return ErrNewResourcePermissionDenied
		}
		return fmt.Errorf(
			"running command %s %v: %w; output: %q",
			Command, args, err, string(out),
		)
	}

	return nil
}
