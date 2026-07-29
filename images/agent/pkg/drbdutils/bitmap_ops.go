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

var TrackBitmapArgs = func(resource string, peerNodeID uint8, volume uint, start bool) []string {
	args := []string{
		"track-bitmap",
		resource,
		strconv.FormatUint(uint64(peerNodeID), 10),
		strconv.FormatUint(uint64(volume), 10),
	}
	if start {
		args = append(args, "--start")
	}
	return args
}

func ExecuteTrackBitmap(ctx context.Context, resource string, peerNodeID uint8, volume uint, start bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, TrackBitmapArgs(resource, peerNodeID, volume, start)...)
	_, err := executeCommand(cmd, nil)
	return err
}

var FlushBitmapArgs = func(minor uint) []string {
	return []string{
		"flush-bitmap",
		strconv.FormatUint(uint64(minor), 10),
	}
}

func ExecuteFlushBitmap(ctx context.Context, minor uint) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, FlushBitmapArgs(minor)...)
	_, err := executeCommand(cmd, nil)
	return err
}
