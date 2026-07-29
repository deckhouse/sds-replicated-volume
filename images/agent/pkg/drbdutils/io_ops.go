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

var SuspendIOArgs = func(minor uint) []string {
	return []string{
		"suspend-io",
		strconv.FormatUint(uint64(minor), 10),
	}
}

func ExecuteSuspendIO(ctx context.Context, minor uint) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, SuspendIOArgs(minor)...)
	_, err := executeCommand(cmd, nil)
	return err
}

var ResumeIOArgs = func(minor uint) []string {
	return []string{
		"resume-io",
		strconv.FormatUint(uint64(minor), 10),
	}
}

func ExecuteResumeIO(ctx context.Context, minor uint) error {
	cmd := ExecCommandContext(ctx, DRBDSetupCommand, ResumeIOArgs(minor)...)
	_, err := executeCommand(cmd, nil)
	return err
}
