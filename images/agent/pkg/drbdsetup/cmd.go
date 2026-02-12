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
	"io"
	"os/exec"
)

// Cmd abstracts command execution for testing.
type Cmd interface {
	CombinedOutput() ([]byte, error)
	// StdoutPipe returns a pipe for reading stdout (for streaming commands like events2).
	StdoutPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
}

// ExecCommandContextFactory creates a Cmd for the given command and arguments.
type ExecCommandContextFactory func(ctx context.Context, name string, arg ...string) Cmd

// ExecCommandContext is overridable for testing purposes.
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

func (r *execCmd) CombinedOutput() ([]byte, error) { return (*exec.Cmd)(r).CombinedOutput() }
func (r *execCmd) StdoutPipe() (io.ReadCloser, error) {
	return (*exec.Cmd)(r).StdoutPipe()
}
func (r *execCmd) Start() error { return (*exec.Cmd)(r).Start() }
func (r *execCmd) Wait() error  { return (*exec.Cmd)(r).Wait() }

// errToExitCode extracts exit code from error if available.
func errToExitCode(err error) int {
	type exitCode interface{ ExitCode() int }

	if errWithExitCode, ok := err.(exitCode); ok {
		return errWithExitCode.ExitCode()
	}

	return 0
}

// withOutput wraps an error with command output if present.
func withOutput(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w; output: %q", err, string(out))
}
