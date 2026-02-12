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

package fake

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type Exec struct {
	cmds []*ExpectedCmd
}

func (b *Exec) ExpectCommands(cmds ...*ExpectedCmd) {
	b.cmds = append(b.cmds, cmds...)
}

func (b *Exec) Setup(t *testing.T) {
	t.Helper()

	tmp := drbdsetup.ExecCommandContext

	i := 0

	drbdsetup.ExecCommandContext = func(_ context.Context, name string, args ...string) drbdsetup.Cmd {
		if len(b.cmds) <= i {
			t.Fatalf("expected %d command executions, got more", len(b.cmds))
		}
		cmd := b.cmds[i]

		if !cmd.Matches(name, args...) {
			t.Fatalf("ExecCommandContext was called with unexpected arguments (call index %d)", i)
		}

		i++
		return cmd
	}

	t.Cleanup(func() {
		// actual cleanup
		drbdsetup.ExecCommandContext = tmp

		// assert all commands executed
		if i != len(b.cmds) {
			t.Errorf("expected %d command executions, got %d", len(b.cmds), i)
		}
	})
}

type ExpectedCmd struct {
	Name string
	Args []string

	ResultOutput []byte
	ResultErr    error

	// For streaming commands (events2)
	stdoutReader io.ReadCloser
}

var _ drbdsetup.Cmd = &ExpectedCmd{}

func (c *ExpectedCmd) Matches(name string, args ...string) bool {
	return c.Name == name && slices.Equal(c.Args, args)
}

func (c *ExpectedCmd) CombinedOutput() ([]byte, error) {
	return c.ResultOutput, c.ResultErr
}

func (c *ExpectedCmd) StdoutPipe() (io.ReadCloser, error) {
	if c.stdoutReader != nil {
		return c.stdoutReader, nil
	}
	// Default: return ResultOutput as a reader
	return io.NopCloser(bytes.NewBuffer(c.ResultOutput)), nil
}

func (c *ExpectedCmd) Start() error {
	return nil
}

func (c *ExpectedCmd) Wait() error {
	return c.ResultErr
}

// SetStdoutReader allows tests to provide a custom reader for streaming commands.
func (c *ExpectedCmd) SetStdoutReader(r io.ReadCloser) {
	c.stdoutReader = r
}

type ExitErr struct{ Code int }

func (e ExitErr) Error() string { return "ExitErr" }
func (e ExitErr) ExitCode() int { return e.Code }
