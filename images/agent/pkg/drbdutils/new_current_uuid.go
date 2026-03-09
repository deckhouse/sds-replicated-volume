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
	"strconv"
)

// NewCurrentUUIDArgs returns the arguments for drbdsetup new-current-uuid command.
var NewCurrentUUIDArgs = func(minor uint, clearBitmap, forceResync bool) []string {
	args := []string{
		"new-current-uuid",
		strconv.FormatUint(uint64(minor), 10),
	}
	if clearBitmap {
		args = append(args, "--clear-bitmap")
	}
	if forceResync {
		args = append(args, "--force-resync")
	}
	return args
}

// ExecuteNewCurrentUUID generates a new current UUID for a DRBD device.
// clearBitmap: skip initial resync (marks all nodes UpToDate)
// forceResync: force resync from this node to peers
func ExecuteNewCurrentUUID(ctx context.Context, minor uint, clearBitmap, forceResync bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, NewCurrentUUIDArgs(minor, clearBitmap, forceResync)...)
	_, err := executeCommand(cmd, nil)
	return err
}
