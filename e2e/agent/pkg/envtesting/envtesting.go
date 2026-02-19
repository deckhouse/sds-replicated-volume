package envtesting

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

// TCommon mirrors the public API of *testing.T, excluding Run.
type TCommon interface {
	Cleanup(func())
	Context() context.Context
	Deadline() (deadline time.Time, ok bool)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fail()
	FailNow()
	Failed() bool
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Helper()
	Log(args ...any)
	Logf(format string, args ...any)
	Name() string
	Parallel()
	Setenv(key, value string)
	Skip(args ...any)
	Skipf(format string, args ...any)
	SkipNow()
	Skipped() bool
	TempDir() string
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

	Options(target any)
	Scope() E
	ScopeWithTimeout(timeout time.Duration) E
	DiscardErrors()
	Close()
}

// New creates an E backed by t. It reads the JSON config file whose path is
// taken from the E2E_CONFIG_PATH environment variable (defaults to ".env.json").
// sections may be provided directly; when nil, the config file is read.
func New[T interface {
	TRun[T]
	TCommon
}](t T, sections map[string]json.RawMessage) E {
	if sections == nil {
		path := os.Getenv("E2E_CONFIG_PATH")
		if path == "" {
			path = ".env.json"
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading config from %s: %v", path, err)
		}

		if err := json.Unmarshal(data, &sections); err != nil {
			t.Fatalf("parsing config sections from %s: %v", path, err)
		}
	}

	return newEImpl(t, sections)
}

// eImpl is the single concrete implementation of E.
type eImpl struct {
	TCommon

	runFn    func(name string, fn func(E)) bool
	sections map[string]json.RawMessage

	parent    E
	ctx       context.Context
	cancelCtx context.CancelFunc

	errors   []string
	failed   bool
	cleanups []func()

	closed    bool
	closingUp bool
}

var _ E = (*eImpl)(nil)

func newEImpl[T interface {
	TRun[T]
	TCommon
}](t T, sections map[string]json.RawMessage) *eImpl {
	e := &eImpl{
		TCommon:  t,
		sections: sections,
	}
	e.runFn = newRunFn(t, sections)
	t.Cleanup(e.Close)
	return e
}

func newRunFn[T interface {
	TRun[T]
	TCommon
}](t T, sections map[string]json.RawMessage) func(string, func(E)) bool {
	return func(name string, fn func(E)) bool {
		return t.Run(name, func(childT T) {
			child := newEImpl(childT, sections)
			fn(child)
		})
	}
}

// Run creates a subtest. The child E has its own scope that auto-closes when
// the subtest finishes.
func (e *eImpl) Run(name string, fn func(E)) bool {
	return e.runFn(name, fn)
}

// Options unmarshals the config section whose key matches the Go type name of
// *target. target must be a pointer to a named type.
func (e *eImpl) Options(target any) {
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

// Error accumulates an error in the scope instead of sending it to T.
func (e *eImpl) Error(args ...any) {
	e.errors = append(e.errors, fmt.Sprint(args...))
	e.failed = true
}

// Errorf accumulates a formatted error in the scope.
func (e *eImpl) Errorf(format string, args ...any) {
	e.errors = append(e.errors, fmt.Sprintf(format, args...))
	e.failed = true
}

// Fail marks the scope as failed without recording an error message.
func (e *eImpl) Fail() {
	e.failed = true
}

// Failed returns true if the scope has accumulated any errors.
func (e *eImpl) Failed() bool {
	return e.failed
}

// Cleanup registers a cleanup function on this scope's own list.
func (e *eImpl) Cleanup(f func()) {
	e.cleanups = append(e.cleanups, f)
}

// Context returns the scope's context. During Close, it returns the parent's
// context so that cleanup operations are not affected by an expired timeout.
func (e *eImpl) Context() context.Context {
	if e.parent == nil {
		return e.TCommon.Context()
	}
	if e.closingUp {
		return e.parent.Context()
	}
	return e.ctx
}

// Scope creates a child E with its own error buffer and cleanup list, sharing
// the parent's context. The child auto-closes when the parent's cleanups run.
func (e *eImpl) Scope() E {
	child := &eImpl{
		TCommon:  e.TCommon,
		parent:   e,
		ctx:      e.Context(),
		sections: e.sections,
		runFn:    e.runFn,
	}
	e.Cleanup(child.Close)
	return child
}

// ScopeWithTimeout creates a child E with its own error buffer, cleanup list,
// and a timeout-bounded context. The child auto-closes when the parent's
// cleanups run.
func (e *eImpl) ScopeWithTimeout(timeout time.Duration) E {
	ctx, cancel := context.WithTimeout(e.Context(), timeout)
	child := &eImpl{
		TCommon:   e.TCommon,
		parent:    e,
		ctx:       ctx,
		cancelCtx: cancel,
		sections:  e.sections,
		runFn:     e.runFn,
	}
	e.Cleanup(child.Close)
	return child
}

// DiscardErrors logs each accumulated error and clears the buffer. The scope
// remains usable -- new errors can accumulate after discard.
func (e *eImpl) DiscardErrors() {
	for _, msg := range e.errors {
		e.TCommon.Logf("discarded error: %s", msg)
	}
	e.errors = nil
	e.failed = false
}

// Close is idempotent. It runs cleanups in LIFO order, sends accumulated
// errors (including any produced during cleanup) to the parent (or T for
// root), cancels the timeout context if any, and re-panics if a cleanup
// panicked.
func (e *eImpl) Close() {
	if e.closed {
		return
	}
	e.closed = true

	// Run cleanups in LIFO first so that cleanup-produced errors are captured.
	// Each runs in its own goroutine to survive panics and runtime.Goexit.
	e.closingUp = true
	var firstPanic any
	for i := len(e.cleanups) - 1; i >= 0; i-- {
		normal, panicked, panicVal := runCleanupFunc(e.cleanups[i])
		if panicked {
			e.TCommon.Logf("cleanup panic: %v", panicVal)
			if firstPanic == nil {
				firstPanic = panicVal
			}
		}
		if !normal && !panicked {
			e.TCommon.Logf("cleanup called runtime.Goexit")
		}
	}
	e.closingUp = false

	// Send all accumulated errors (pre-close + cleanup-produced) to parent or T.
	if e.parent != nil {
		for _, msg := range e.errors {
			e.parent.Error(msg)
		}
		if e.failed && len(e.errors) == 0 {
			e.parent.Fail()
		}
	} else {
		for _, msg := range e.errors {
			e.TCommon.Error(msg)
		}
		if e.failed && len(e.errors) == 0 {
			e.TCommon.Fail()
		}
	}
	e.errors = nil

	if e.cancelCtx != nil {
		e.cancelCtx()
	}

	if firstPanic != nil {
		panic(firstPanic)
	}
}

// runCleanupFunc executes f in a goroutine to survive both panic and
// runtime.Goexit, ensuring the caller can continue running remaining cleanups.
func runCleanupFunc(f func()) (normalReturn, panicked bool, panicVal any) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if !normalReturn {
				panicVal = recover()
				panicked = panicVal != nil
			}
		}()
		f()
		normalReturn = true
	}()
	wg.Wait()
	return
}
