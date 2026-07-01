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

package ctrlexec

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
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

type ExecCommandContextFactory func(ctx context.Context, name string, args ...string) Cmd

// ExecCommandContext returns an ExecCommandContextFactory backed by
// exec.CommandContext. If configure is non-nil, it is invoked on each
// freshly constructed *exec.Cmd before the Cmd is returned, giving the
// caller a chance to set fields such as WaitDelay, Dir, Env, Stderr, or
// SysProcAttr.
//
// configure runs synchronously inside the factory and MUST NOT block; it
// is called for every invocation of the returned factory.
//
// Setting WaitDelay > 0 here is strongly recommended for any command-line
// tool that may have grandchildren inheriting stdout/stderr pipes (most
// shell-invoked utilities qualify), because WaitDelay bounds Cmd.Wait()'s
// patience for those pipes to close after the process exits. Without it,
// Cmd.Wait() can block until the grandchild itself dies, which may be
// never.
func ExecCommandContext(configure func(*exec.Cmd)) ExecCommandContextFactory {
	return func(ctx context.Context, name string, args ...string) Cmd {
		c := exec.CommandContext(ctx, name, args...)
		if configure != nil {
			configure(c)
		}
		return c
	}
}

// WithTimeout returns a factory that bounds the lifetime of each command's
// context to timeout. The resulting Cmd cancels that context after
// CombinedOutput or Wait returns, releasing the timer goroutine.
func WithTimeout(timeout time.Duration, target ExecCommandContextFactory) ExecCommandContextFactory {
	return func(ctx context.Context, name string, args ...string) Cmd {
		tctx, cancel := context.WithTimeout(ctx, timeout)
		return &timeoutCmd{
			Cmd:    target(tctx, name, args...),
			cancel: cancel,
		}
	}
}

// timeoutCmd wraps a Cmd to invoke cancel exactly once, when CombinedOutput
// or Wait returns. If the caller calls Start without ever calling Wait, the
// context will be released when its own timeout fires; nothing leaks
// permanently.
type timeoutCmd struct {
	Cmd
	cancel context.CancelFunc
	once   sync.Once
}

func (c *timeoutCmd) cleanup() { c.once.Do(c.cancel) }

func (c *timeoutCmd) CombinedOutput() ([]byte, error) {
	defer c.cleanup()
	return c.Cmd.CombinedOutput()
}

func (c *timeoutCmd) Wait() error {
	defer c.cleanup()
	return c.Cmd.Wait()
}
