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
	"io"
	"os/exec"
	"strings"
)

// CombinedOutputRunner is the minimal interface needed by executeCommand.
type CombinedOutputRunner interface {
	CombinedOutput() ([]byte, error)
	String() string
}

// Cmd is the full command interface used for both one-shot and streaming
// (e.g. events2) command execution.
type Cmd interface {
	CombinedOutputRunner
	StdoutPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
}

type ExecCommandContextFactory func(ctx context.Context, name string, arg ...string) Cmd

func defaultExecCommandContext(ctx context.Context, name string, arg ...string) Cmd {
	return exec.CommandContext(ctx, name, arg...)
}

var ExecCommandContext ExecCommandContextFactory = defaultExecCommandContext

var DRBDSetupCommand = "drbdsetup"

var DRBDMetaCommand = "drbdmeta"

type KnownError struct {
	ExitCode        int
	OutputSubstring string
	JoinErr         error
}

func errToExitCode(err error) int {
	type exitCode interface{ ExitCode() int }

	if errWithExitCode, ok := err.(exitCode); ok {
		return errWithExitCode.ExitCode()
	}

	return 0
}

func withOutput(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w; output: %q", err, string(out))
}

func executeCommand(
	cmd CombinedOutputRunner,
	knownErrors []KnownError,
) ([]byte, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitCode := errToExitCode(err)
		outStr := string(out)

		for _, ke := range knownErrors {
			if ke.ExitCode == exitCode && strings.Contains(outStr, ke.OutputSubstring) {
				err = errors.Join(ke.JoinErr, err)
				break
			}
		}

		return nil, fmt.Errorf("running command %s: %w", cmd, withOutput(err, out))
	}

	return out, nil
}
