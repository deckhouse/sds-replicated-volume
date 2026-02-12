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
	"strconv"
)

var (
	ErrNewCurrentUUIDWrongDiskState   = errors.New("wrong disk state for new-current-uuid")
	ErrNewCurrentUUIDStateChangeError = errors.New("state change error")
	ErrNewCurrentUUIDNeedProtocolC    = errors.New("need protocol C for new-current-uuid")
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
func ExecuteNewCurrentUUID(ctx context.Context, minor uint, clearBitmap, forceResync bool) (err error) {
	args := NewCurrentUUIDArgs(minor, clearBitmap, forceResync)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		switch errToExitCode(err) {
		case 10:
			err = ErrNewCurrentUUIDWrongDiskState
		case 11:
			err = ErrNewCurrentUUIDStateChangeError
		}
		return withOutput(err, out)
	}

	return nil
}
