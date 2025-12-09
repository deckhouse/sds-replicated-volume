package drbdadm

import (
	"context"
	"io"
	"os/exec"
)

type cmd interface {
	CombinedOutput() ([]byte, error)
	SetStderr(io.Writer)
	Run() error
}

// overridable for testing purposes
var ExecCommandContext = func(ctx context.Context, name string, arg ...string) cmd {
	return (*execCmd)(exec.CommandContext(ctx, name, arg...))
}

// dummy decorator to isolate from [exec.Cmd] struct fields
type execCmd exec.Cmd

var _ cmd = &execCmd{}

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
