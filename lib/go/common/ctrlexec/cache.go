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
	"hash/maphash"
	"io"
	"sync"
)

var seed = maphash.MakeSeed()

var (
	runningMu       sync.Mutex
	runningCommands = map[uint64]*cacheEntry{}
)

type cacheEntry struct {
	done   chan struct{}
	output []byte
	err    error
}

// WithCache wraps target with a process-wide deduplicating cache for in-flight
// command executions.
//
// When the returned factory is invoked, the resulting Cmd executes the target
// Cmd in a background goroutine on the first call to Start or CombinedOutput.
// The target is built with a context detached from the caller's, so the
// underlying process is not killed when the caller's context expires; the
// caller's context only governs *waiting* for the result.
//
// Concurrent invocations sharing the same (name, args...) tuple are
// deduplicated: only one underlying process runs and all callers wait on its
// result. The cache entry is removed once the process completes.
//
// This is intended for command-line tools that may block in uninterruptible
// kernel state (D-state). A Kubernetes controller can return from Reconcile
// when its context expires without leaking a goroutine blocked on the stuck
// process; a subsequent reconcile attaches to the still-running command
// rather than spawning a duplicate.
func WithCache(target ExecCommandContextFactory) ExecCommandContextFactory {
	return func(ctx context.Context, name string, args ...string) Cmd {
		var h maphash.Hash
		h.SetSeed(seed)

		h.WriteString(name)
		h.WriteByte(0)

		for _, arg := range args {
			h.WriteString(arg)
			h.WriteByte(0)
		}

		// Detach from caller ctx: caller cancellation must not kill the
		// running process. A later reconcile attaches to it instead.
		return &cachedCmd{
			ctx:    ctx,
			target: target(context.WithoutCancel(ctx), name, args...),
			key:    h.Sum64(),
		}
	}
}

type cachedCmd struct {
	ctx    context.Context
	target Cmd
	key    uint64

	once  sync.Once
	entry *cacheEntry
}

var _ Cmd = (*cachedCmd)(nil)

func (c *cachedCmd) String() string {
	return c.target.String()
}

func (c *cachedCmd) StdoutPipe() (io.ReadCloser, error) {
	return c.target.StdoutPipe()
}

// ensureStarted attaches to (or creates) the shared cache entry and ensures
// the target Cmd is running in a background goroutine. It is safe to call
// multiple times; only the first call has an effect on this instance.
func (c *cachedCmd) ensureStarted() *cacheEntry {
	c.once.Do(func() {
		runningMu.Lock()
		if existing, ok := runningCommands[c.key]; ok {
			c.entry = existing
			runningMu.Unlock()
			return
		}

		e := &cacheEntry{done: make(chan struct{})}
		runningCommands[c.key] = e
		runningMu.Unlock()

		c.entry = e

		go func() {
			output, err := c.target.CombinedOutput()

			// The writes below are safe without a lock: e.output and e.err are
			// only read after <-e.done returns, and close(e.done) provides the
			// happens-before edge.
			e.output = output
			e.err = err

			runningMu.Lock()
			delete(runningCommands, c.key)
			runningMu.Unlock()

			close(e.done)
		}()
	})
	return c.entry
}

func (c *cachedCmd) CombinedOutput() ([]byte, error) {
	e := c.ensureStarted()
	select {
	case <-e.done:
		return e.output, e.err
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

func (c *cachedCmd) Start() error {
	c.ensureStarted()
	return nil
}

func (c *cachedCmd) Wait() error {
	e := c.ensureStarted()
	select {
	case <-e.done:
		return e.err
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
