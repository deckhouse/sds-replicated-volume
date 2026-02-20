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

package drbdadm

import (
	"context"
)

func ExecuteUp(ctx context.Context, resource string) CommandError {
	args := UpArgs(resource)
	cmd := ExecCommandContext(ctx, Command, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return &commandError{
			error:           err,
			commandWithArgs: append([]string{Command}, args...),
			output:          string(out),
			exitCode:        errToExitCode(err),
		}
	}

	return nil
}
