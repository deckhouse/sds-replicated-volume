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

package envtesting

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"time"
)

// TDirect contains methods that always pass through to *testing.T.
// E never overrides these.
type TDirect interface {
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	FailNow()
	Log(args ...any)
	Logf(format string, args ...any)
	Helper()
	Skip(args ...any)
	Skipf(format string, args ...any)
	SkipNow()
	Name() string
	Parallel()
	Setenv(key, value string)
	Skipped() bool
	TempDir() string
	Deadline() (deadline time.Time, ok bool)
}

// TOverridable contains methods that E overrides with scope-local behavior.
type TOverridable interface {
	Cleanup(func())
	Context() context.Context
	Error(args ...any)
	Errorf(format string, args ...any)
	Fail()
	Failed() bool
}

// TCommon is the full interface matching *testing.T's public API (excluding Run).
type TCommon interface {
	TDirect
	TOverridable
}

// TRun is the generic subtest interface. *testing.T satisfies TRun[*testing.T].
type TRun[T any] interface {
	Run(name string, fn func(T)) bool
}

// E is the environment test context. It embeds TCommon for direct delegation
// of irreversible methods (Fatal, Log, etc.) and overrides reversible methods
// (Error, Errorf, Fail, Failed) to accumulate errors within a scope.
type E interface {
	TCommon
	TRun[E]

	Options(targets ...any)
	Scope() E
	ScopeWithTimeout(timeout time.Duration) E
	DiscardErrors()
	Close()
}

// New creates an E backed by t. It reads the JSON config file whose path is
// taken from the E2E_CONFIG_PATH environment variable (defaults to ".env.json").
func New[T interface {
	TRun[T]
	TCommon
}](t T) E {
	path := os.Getenv("E2E_CONFIG_PATH")
	if path == "" {
		path = ".env.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config from %s: %v", path, err)
	}

	var sections map[string]json.RawMessage
	if err := json.Unmarshal(data, &sections); err != nil {
		t.Fatalf("parsing config sections from %s: %v", path, err)
	}

	return newRoot(t, sections)
}

// errorRecord stores arguments for a deferred Error or Errorf call.
type errorRecord struct {
	format *string
	args   []any
}

// scope is the single concrete implementation of E.
// TCommon is either the underlying T (root/subtest) or the parent E (scope).
type scope struct {
	TCommon

	mu sync.RWMutex

	runFn    func(name string, fn func(E)) bool
	sections map[string]json.RawMessage

	ctx       context.Context
	cancelCtx context.CancelFunc

	errors   []errorRecord
	failed   bool
	cleanups []func()

	closed bool
}

var _ E = (*scope)(nil)

func newRoot[T interface {
	TRun[T]
	TCommon
}](t T, sections map[string]json.RawMessage) *scope {
	ctx, cancel := context.WithCancel(t.Context())
	runFn := newRunFn(t, sections)
	return newChild(t, ctx, cancel, sections, runFn)
}

func newRunFn[T interface {
	TRun[T]
	TCommon
}](t T, sections map[string]json.RawMessage) func(string, func(E)) bool {
	return func(name string, fn func(E)) bool {
		return t.Run(name, func(childT T) {
			fn(newRoot(childT, sections))
		})
	}
}

func newChild(parent TCommon, ctx context.Context, cancelCtx context.CancelFunc, sections map[string]json.RawMessage, runFn func(string, func(E)) bool) *scope {
	child := &scope{
		TCommon:   parent,
		ctx:       ctx,
		cancelCtx: cancelCtx,
		sections:  sections,
		runFn:     runFn,
	}
	parent.Cleanup(child.Close)
	return child
}

// Run creates a subtest. The child E has its own scope that auto-closes when
// the subtest finishes.
func (e *scope) Run(name string, fn func(E)) bool {
	return e.runFn(name, fn)
}

// Options unmarshals one or more config sections. For each target, the config
// section key is determined by the Go type name of *target. Each target must
// be a pointer to a named type.
func (e *scope) Options(targets ...any) {
	for _, target := range targets {
		typ := reflect.TypeOf(target)
		if typ.Kind() != reflect.Pointer {
			e.TCommon.Fatalf("Options: target must be a pointer, got %T", target)
		}

		elemType := typ.Elem()
		typeName := elemType.Name()
		if typeName == "" {
			e.TCommon.Fatalf("Options: target must be a pointer to a named type, got %T", target)
		}

		raw, ok := e.sections[typeName]
		if !ok {
			e.TCommon.Fatalf("Options: section %q not found", typeName)
		}

		if err := json.Unmarshal(raw, target); err != nil {
			e.TCommon.Fatalf("Options: unmarshalling section %q: %v", typeName, err)
		}
	}
}

// Error accumulates an error in the scope instead of sending it to T.
func (e *scope) Error(args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errors = append(e.errors, errorRecord{args: args})
	e.failed = true
}

// Errorf accumulates a formatted error in the scope.
func (e *scope) Errorf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errors = append(e.errors, errorRecord{format: &format, args: args})
	e.failed = true
}

// Fail marks the scope as failed without recording an error message.
func (e *scope) Fail() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failed = true
}

// Failed returns true if the scope has accumulated any errors.
func (e *scope) Failed() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.failed
}

// Cleanup registers a cleanup function on this scope's own list.
func (e *scope) Cleanup(f func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanups = append(e.cleanups, f)
}

// Context returns the scope's context.
func (e *scope) Context() context.Context {
	return e.ctx
}

// Scope creates a child E with its own error buffer and cleanup list. The
// child gets a derived context that is cancelled before its cleanups run.
func (e *scope) Scope() E {
	ctx, cancel := context.WithCancel(e.Context())
	return newChild(e, ctx, cancel, e.sections, e.runFn)
}

// ScopeWithTimeout creates a child E with its own error buffer, cleanup list,
// and a timeout-bounded context.
func (e *scope) ScopeWithTimeout(timeout time.Duration) E {
	ctx, cancel := context.WithTimeout(e.Context(), timeout)
	return newChild(e, ctx, cancel, e.sections, e.runFn)
}

// DiscardErrors logs each accumulated error and clears the buffer. The scope
// remains usable -- new errors can accumulate after discard.
func (e *scope) DiscardErrors() {
	e.mu.Lock()
	errors := e.errors
	e.errors = nil
	e.failed = false
	e.mu.Unlock()

	for _, rec := range errors {
		if rec.format != nil {
			e.TCommon.Logf("discarded: "+*rec.format, rec.args...)
		} else {
			e.TCommon.Log(append([]any{"discarded:"}, rec.args...)...)
		}
	}
}

// Close is idempotent. It cancels the context, runs cleanups in LIFO order
// (surviving panics and runtime.Goexit via recursion, matching testing.T),
// sends accumulated errors (including any produced during cleanup) to TCommon,
// and re-panics with the first panic value.
func (e *scope) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.mu.Unlock()

	// Cancel context before cleanups, matching testing.T behavior.
	cancel := e.cancelCtx
	e.cancelCtx = func() {}
	cancel()

	// Run cleanups. Lock is NOT held -- each cleanup may call e.Error(),
	// e.Cleanup(), etc. which lock independently. Matching testing.T.
	panicVal := e.runCleanups()

	// Snapshot and clear accumulated errors under lock.
	e.mu.Lock()
	errors := e.errors
	failed := e.failed
	e.errors = nil
	e.failed = false
	e.mu.Unlock()

	// Replay errors to parent (TCommon). Parent handles its own locking.
	for _, rec := range errors {
		if rec.format != nil {
			e.TCommon.Errorf(*rec.format, rec.args...)
		} else {
			e.TCommon.Error(rec.args...)
		}
	}
	if failed && len(errors) == 0 {
		e.TCommon.Fail()
	}

	if panicVal != nil {
		panic(panicVal)
	}
}

// runCleanups executes cleanup functions in LIFO order using recursion+defer
// to survive panics and runtime.Goexit, matching testing.T.runCleanup.
// The mutex is locked only briefly to pop a cleanup, then released before
// calling it -- so callbacks can safely call e.Error(), e.Cleanup(), etc.
func (e *scope) runCleanups() (panicVal any) {
	defer func() {
		e.mu.RLock()
		remaining := len(e.cleanups)
		e.mu.RUnlock()
		if remaining > 0 {
			if p := e.runCleanups(); p != nil && panicVal == nil {
				panicVal = p
			}
		}
	}()

	for {
		e.mu.Lock()
		if len(e.cleanups) == 0 {
			e.mu.Unlock()
			return nil
		}
		last := len(e.cleanups) - 1
		cleanup := e.cleanups[last]
		e.cleanups = e.cleanups[:last]
		e.mu.Unlock()

		cleanup()
	}
}
