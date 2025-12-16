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

package fake

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

type Exec struct {
	cmds []*ExpectedCmd
}

func (b *Exec) ExpectCommands(cmds ...*ExpectedCmd) {
	b.cmds = append(b.cmds, cmds...)
}

func (b *Exec) Setup(t *testing.T) {
	t.Helper()

	tmp := drbdadm.ExecCommandContext

	i := 0

	drbdadm.ExecCommandContext = func(ctx context.Context, name string, args ...string) drbdadm.Cmd {
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
		drbdadm.ExecCommandContext = tmp

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

	stderr io.Writer
}

var _ drbdadm.Cmd = &ExpectedCmd{}

func (c *ExpectedCmd) Matches(name string, args ...string) bool {
	return c.Name == name && slices.Equal(c.Args, args)
}

func (c *ExpectedCmd) CombinedOutput() ([]byte, error) {
	return c.ResultOutput, c.ResultErr
}

func (c *ExpectedCmd) SetStderr(w io.Writer) {
	c.stderr = w
}

func (c *ExpectedCmd) Run() error {
	if c.stderr != nil {
		io.Copy(c.stderr, bytes.NewBuffer(c.ResultOutput))
	}
	return c.ResultErr
}

type ExitErr struct{ Code int }

func (e ExitErr) Error() string { return "ExitErr" }
func (e ExitErr) ExitCode() int { return e.Code }
