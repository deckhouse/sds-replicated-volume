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
	ErrResizeBackingNotGrown  = errors.New("backing device not grown")
	ErrResizePeerNoSpace      = errors.New("peer doesn't have space")
	ErrResizeNeedPrimary      = errors.New("need one primary node for resize")
	ErrResizeResourceNotFound = errors.New("resource not found")
)

// ResizeArgs returns the arguments for drbdsetup resize command.
// DRBD re-examines the backing device and uses whatever space is available.
var ResizeArgs = func(minor uint) []string {
	return []string{"resize", fmt.Sprintf("%d", minor)}
}

// ExecuteResize resizes a replicated device after growing backing devices.
// DRBD re-examines the backing device and uses whatever space is available.
func ExecuteResize(ctx context.Context, minor uint) (err error) {
	args := ResizeArgs(minor)
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
			err = ErrResizeBackingNotGrown
		case 11:
			err = ErrResizeNeedPrimary
		case 158:
			err = ErrResizeResourceNotFound
		}
		return withOutput(err, out)
	}

	return nil
}
