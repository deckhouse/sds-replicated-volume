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
	"fmt"
)

var (
	ErrResizeBackingNotGrown = errors.New("backing device not grown")
	ErrResizeNeedPrimary     = errors.New("need one primary node for resize")
)

// ResizeArgs returns the arguments for drbdsetup resize command.
// When sizeBytes > 0, an explicit --size (in 512-byte sectors) is passed
// instead of letting DRBD auto-detect from the backing device.
var ResizeArgs = func(minor uint, sizeBytes int64) []string {
	args := []string{"resize", fmt.Sprintf("%d", minor)}
	if sizeBytes > 0 {
		sectors := sizeBytes / 512
		args = append(args, fmt.Sprintf("--size=%d", sectors))
	}
	return args
}

var ResizeKnownErrors = []KnownError{
	{ExitCode: 10, OutputSubstring: "(111)", JoinErr: ErrResizeBackingNotGrown},
	{ExitCode: 10, OutputSubstring: "(131)", JoinErr: ErrResizeNeedPrimary},
}

// ExecuteResize resizes a replicated device after growing backing devices.
// When sizeBytes > 0, the target usable size is passed explicitly via --size.
func ExecuteResize(ctx context.Context, minor uint, sizeBytes int64) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ResizeArgs(minor, sizeBytes)...)
	_, err := executeCommand(cmd, ResizeKnownErrors)
	return err
}
