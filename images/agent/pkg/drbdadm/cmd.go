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
	"io"
	"os/exec"
)

type Cmd interface {
	CombinedOutput() ([]byte, error)
	SetStderr(io.Writer)
	Run() error
}

type ExecCommandContextFactory func(ctx context.Context, name string, arg ...string) Cmd

// overridable for testing purposes
var ExecCommandContext ExecCommandContextFactory = func(
	ctx context.Context,
	name string,
	arg ...string,
) Cmd {
	return (*execCmd)(exec.CommandContext(ctx, name, arg...))
}

// dummy decorator to isolate from [exec.Cmd] struct fields
type execCmd exec.Cmd

var _ Cmd = &execCmd{}

func (r *execCmd) Run() error                      { return (*exec.Cmd)(r).Run() }
func (r *execCmd) SetStderr(w io.Writer)           { (*exec.Cmd)(r).Stderr = w }
func (r *execCmd) CombinedOutput() ([]byte, error) { return (*exec.Cmd)(r).CombinedOutput() }
func (r *execCmd) ProcessStateExitCode() int       { return (*exec.Cmd)(r).ProcessState.ExitCode() }

// helper to isolate from [exec.ExitError]
func errToExitCode(err error) int {
	type exitCode interface{ ExitCode() int }

	if errWithExitCode, ok := err.(exitCode); ok {
		return errWithExitCode.ExitCode()
	}

	return 0
}
