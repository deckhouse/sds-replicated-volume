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

// DetachArgs returns the arguments for drbdsetup detach command.
// When diskless is true, --diskless is appended so that the kernel keeps the device
// marked as an intentionally diskless client instead of an unintentionally diskless one.
var DetachArgs = func(minor uint, diskless bool) []string {
	args := []string{
		"detach",
		strconv.FormatUint(uint64(minor), 10),
	}
	if diskless {
		args = append(args, "--diskless")
	}
	return args
}

// ExecuteDetach detaches the backing device from a replicated device.
// When diskless is true, --diskless is passed to mark the device as an intentionally diskless client.
func ExecuteDetach(ctx context.Context, minor uint, diskless bool) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, DetachArgs(minor, diskless)...)
	_, err := executeCommand(cmd, nil)
	return err
}
