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
	"fmt"
	"strconv"
)

// DetachArgs returns the arguments for drbdsetup detach command.
var DetachArgs = func(minor uint) []string {
	return []string{
		"detach",
		strconv.FormatUint(uint64(minor), 10),
	}
}

// ExecuteDetach detaches the backing device from a replicated device.
func ExecuteDetach(ctx context.Context, minor uint) (err error) {
	args := DetachArgs(minor)
	cmd := ExecCommandContext(ctx, Command, args...)

	defer func() {
		if err != nil {
			err = fmt.Errorf("running command %s %v: %w", Command, args, err)
		}
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		return withOutput(err, out)
	}

	return nil
}
