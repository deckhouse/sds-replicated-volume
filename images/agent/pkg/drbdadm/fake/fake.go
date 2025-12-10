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
	t    *testing.T
	cmds []cmd
}

func NewExec(t *testing.T) *Exec {
	return &Exec{t: t}
}

func (b *Exec) ExpectCommand(name string, args []string, resultOutput []byte, resultErr error) {
	b.cmds = append(b.cmds, cmd{
		name:         name,
		args:         args,
		resultOutput: resultOutput,
		resultErr:    resultErr,
	})
}

func (b *Exec) Setup() {
	b.t.Helper()

	tmp := drbdadm.ExecCommandContext

	i := 0

	drbdadm.ExecCommandContext = func(ctx context.Context, name string, args ...string) drbdadm.Cmd {
		if len(b.cmds) <= i {
			b.t.Fatalf("expected %d command executions, got more", len(b.cmds))
		}
		cmd := &b.cmds[i]

		if !cmd.Matches(name, args...) {
			b.t.Fatalf("ExecCommandContext was called with unexpected arguments (call index %d)", i)
		}

		i++
		return cmd
	}

	b.t.Cleanup(func() {
		// actual cleanup
		drbdadm.ExecCommandContext = tmp

		// assert all commands executed
		if i != len(b.cmds) {
			b.t.Errorf("expected %d command executions, got %d", len(b.cmds), i)
		}
	})
}

type cmd struct {
	name string
	args []string

	resultOutput []byte
	resultErr    error

	stderr io.Writer
}

var _ drbdadm.Cmd = &cmd{}

func (c *cmd) Matches(name string, args ...string) bool {
	return c.name == name && slices.Equal(c.args, args)
}

func (c *cmd) CombinedOutput() ([]byte, error) {
	return c.resultOutput, c.resultErr
}

func (c *cmd) SetStderr(w io.Writer) {
	c.stderr = w
}

func (c *cmd) Run() error {
	if c.stderr != nil {
		io.Copy(c.stderr, bytes.NewBuffer(c.resultOutput))
	}
	return c.resultErr
}

type ExitErr struct{ Code int }

func (e ExitErr) Error() string { return "ExitErr" }
func (e ExitErr) ExitCode() int { return e.Code }
