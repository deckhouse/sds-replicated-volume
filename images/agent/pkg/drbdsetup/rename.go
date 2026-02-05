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
	"fmt"
)

// RenameArgs returns the arguments for drbdsetup rename-resource command.
var RenameArgs = func(oldName, newName string) []string {
	return []string{"rename-resource", oldName, newName}
}

// ExecuteRename renames a DRBD resource locally.
// This is a local operation only - it doesn't affect peer nodes.
func ExecuteRename(ctx context.Context, oldName, newName string) (err error) {
	args := RenameArgs(oldName, newName)
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
