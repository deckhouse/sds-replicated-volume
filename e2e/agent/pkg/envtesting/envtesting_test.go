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

package envtesting_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// mockT satisfies TRun[*mockT] + TCommon for unit testing.
type mockT struct {
	ctx      context.Context
	name     string
	cleanups []func()
	logs     []string
	errors   []string
	fatals   []string
	failed   bool
	skipped  bool
}

func newMockT() *mockT {
	return &mockT{ctx: context.Background(), name: "mock"}
}

func (m *mockT) Cleanup(f func())            { m.cleanups = append(m.cleanups, f) }
func (m *mockT) Context() context.Context    { return m.ctx }
func (m *mockT) Deadline() (time.Time, bool) { return time.Time{}, false }
func (m *mockT) Error(args ...any)           { m.errors = append(m.errors, fmt.Sprint(args...)); m.failed = true }
func (m *mockT) Errorf(format string, args ...any) {
	m.errors = append(m.errors, fmt.Sprintf(format, args...))
	m.failed = true
}
func (m *mockT) Fail()        { m.failed = true }
func (m *mockT) FailNow()     { m.failed = true; runtime.Goexit() }
func (m *mockT) Failed() bool { return m.failed }
func (m *mockT) Fatal(args ...any) {
	m.fatals = append(m.fatals, fmt.Sprint(args...))
	m.failed = true
	runtime.Goexit()
}
func (m *mockT) Fatalf(format string, args ...any) {
	m.fatals = append(m.fatals, fmt.Sprintf(format, args...))
	m.failed = true
	runtime.Goexit()
}
func (m *mockT) Helper()         {}
func (m *mockT) Log(args ...any) { m.logs = append(m.logs, fmt.Sprint(args...)) }
func (m *mockT) Logf(format string, args ...any) {
	m.logs = append(m.logs, fmt.Sprintf(format, args...))
}
func (m *mockT) Name() string                     { return m.name }
func (m *mockT) Parallel()                        {}
func (m *mockT) Setenv(_, _ string)               {}
func (m *mockT) Skip(args ...any)                 { m.skipped = true; runtime.Goexit() }
func (m *mockT) Skipf(format string, args ...any) { m.skipped = true; runtime.Goexit() }
func (m *mockT) SkipNow()                         { m.skipped = true; runtime.Goexit() }
func (m *mockT) Skipped() bool                    { return m.skipped }
func (m *mockT) TempDir() string                  { return "" }

func (m *mockT) Run(name string, fn func(*mockT)) bool {
	child := &mockT{ctx: m.ctx, name: m.name + "/" + name}
	fn(child)
	for i := len(child.cleanups) - 1; i >= 0; i-- {
		child.cleanups[i]()
	}
	if child.failed {
		m.failed = true
	}
	return !child.failed
}

// --- Helpers ---

type TestConfig struct {
	Key string `json:"key"`
}

func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("E2E_CONFIG_PATH", path)
}

func newTestE(t *testing.T) (envtesting.E, *mockT) {
	t.Helper()
	writeConfig(t, `{"TestConfig":{"key":"value"}}`)
	m := newMockT()
	return envtesting.New(m), m
}

// --- Options tests ---

func TestOptions(t *testing.T) {
	e, _ := newTestE(t)

	var cfg TestConfig
	e.Options(&cfg)

	if cfg.Key != "value" {
		t.Fatalf("expected key=value, got %q", cfg.Key)
	}
}

func TestOptions_NonPointer(t *testing.T) {
	writeConfig(t, `{}`)
	m := newMockT()
	e := envtesting.New(m)

	done := make(chan struct{})
	go func() {
		defer close(done)
		var cfg TestConfig
		e.Options(cfg)
	}()
	<-done

	if len(m.fatals) == 0 {
		t.Fatal("expected fatal for non-pointer target")
	}
}

func TestOptions_MissingSection(t *testing.T) {
	writeConfig(t, `{}`)
	m := newMockT()
	e := envtesting.New(m)

	done := make(chan struct{})
	go func() {
		defer close(done)
		var cfg TestConfig
		e.Options(&cfg)
	}()
	<-done

	if len(m.fatals) == 0 {
		t.Fatal("expected fatal for missing section")
	}
}

// --- Scope / Cleanup tests ---

func TestScope_CleanupLIFO(t *testing.T) {
	e, _ := newTestE(t)

	var order []int
	child := e.Scope()
	child.Cleanup(func() { order = append(order, 1) })
	child.Cleanup(func() { order = append(order, 2) })
	child.Cleanup(func() { order = append(order, 3) })
	child.Close()

	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Fatalf("expected LIFO order [3 2 1], got %v", order)
	}
}

func TestScope_IndependentFromParent(t *testing.T) {
	e, _ := newTestE(t)

	parentCleaned := false
	e.Cleanup(func() { parentCleaned = true })

	child := e.Scope()
	childCleaned := false
	child.Cleanup(func() { childCleaned = true })
	child.Close()

	if !childCleaned {
		t.Fatal("child cleanup should have run")
	}
	if parentCleaned {
		t.Fatal("parent cleanup should NOT have run from child Close")
	}
}

func TestScope_ContextDerivedFromParent(t *testing.T) {
	e, _ := newTestE(t)

	child := e.Scope()
	defer child.Close()

	if child.Context() == e.Context() {
		t.Fatal("Scope child should have its own derived context, not the same pointer")
	}
	if child.Context().Err() != nil {
		t.Fatal("Scope child context should be valid before Close")
	}
}

func TestScope_ContextCancelledDuringCleanup(t *testing.T) {
	e, _ := newTestE(t)

	child := e.Scope()

	var ctxDuringCleanup context.Context
	child.Cleanup(func() {
		ctxDuringCleanup = child.Context()
	})
	child.Close()

	if ctxDuringCleanup.Err() == nil {
		t.Fatal("Scope context during cleanup should be cancelled (matching testing.T behavior)")
	}
}

func TestScope_ErrorAccumulation(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Error("scope error 1")
	child.Errorf("scope error %d", 2)

	if !child.Failed() {
		t.Fatal("child should be failed after Error/Errorf")
	}
	if m.failed {
		t.Fatal("T should NOT be failed before child Close")
	}

	child.Close()
}

func TestScope_ErrorsSentToParentOnClose(t *testing.T) {
	e, _ := newTestE(t)

	parent := e.Scope()
	child := parent.Scope()
	child.Error("child error")
	child.Close()

	if !parent.Failed() {
		t.Fatal("parent should be failed after child Close sends errors")
	}
}

func TestScope_FailWithoutMessage(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Fail()

	if !child.Failed() {
		t.Fatal("child should be failed")
	}

	child.Close()
	e.Close()

	if !m.failed {
		t.Fatal("T should be failed after root Close propagates Fail()")
	}
}

// --- ScopeWithTimeout tests ---

func TestScopeWithTimeout_HasDeadline(t *testing.T) {
	e, _ := newTestE(t)

	child := e.ScopeWithTimeout(50 * time.Millisecond)
	defer child.Close()

	deadline, ok := child.Context().Deadline()
	if !ok {
		t.Fatal("ScopeWithTimeout child context should have a deadline")
	}
	if time.Until(deadline) > 50*time.Millisecond {
		t.Fatalf("deadline too far in the future: %v", time.Until(deadline))
	}
}

func TestScopeWithTimeout_CloseCancelsContext(t *testing.T) {
	e, _ := newTestE(t)

	child := e.ScopeWithTimeout(10 * time.Second)
	ctx := child.Context()
	child.Close()

	if ctx.Err() != context.Canceled {
		t.Fatalf("expected context.Canceled after Close, got %v", ctx.Err())
	}
}

func TestScopeWithTimeout_ContextCancelledDuringCleanup(t *testing.T) {
	e, _ := newTestE(t)

	child := e.ScopeWithTimeout(10 * time.Second)

	var ctxDuringCleanup context.Context
	child.Cleanup(func() {
		ctxDuringCleanup = child.Context()
	})
	child.Close()

	if ctxDuringCleanup.Err() == nil {
		t.Fatal("context during cleanup should be cancelled (matching testing.T behavior)")
	}
}

func TestScopeWithTimeout_Nested(t *testing.T) {
	e, _ := newTestE(t)

	outer := e.ScopeWithTimeout(10 * time.Second)
	inner := outer.ScopeWithTimeout(50 * time.Millisecond)
	defer outer.Close()
	defer inner.Close()

	outerDeadline, _ := outer.Context().Deadline()
	innerDeadline, _ := inner.Context().Deadline()

	if !innerDeadline.Before(outerDeadline) {
		t.Fatalf("inner deadline (%v) should be before outer deadline (%v)",
			innerDeadline, outerDeadline)
	}
}

// --- DiscardErrors tests ---

func TestDiscardErrors(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Error("will be discarded")
	child.Errorf("also %s", "discarded")

	if !child.Failed() {
		t.Fatal("child should be failed before discard")
	}

	child.DiscardErrors()

	if child.Failed() {
		t.Fatal("child should NOT be failed after discard")
	}

	child.Close()

	if m.failed {
		t.Fatal("T should NOT be failed -- errors were discarded")
	}
	if len(m.logs) < 2 {
		t.Fatalf("expected at least 2 discard log messages, got %d", len(m.logs))
	}
}

func TestDiscardErrors_NewErrorsAfterDiscard(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Error("first")
	child.DiscardErrors()
	child.Error("second")
	child.Close()

	e.Close()

	if !m.failed {
		t.Fatal("T should be failed -- second error was not discarded")
	}
}

// --- Close idempotency ---

func TestClose_Idempotent(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Error("once")
	child.Close()
	child.Close()

	e.Close()

	errorCount := 0
	for _, msg := range m.errors {
		if msg == "once" {
			errorCount++
		}
	}
	if errorCount > 1 {
		t.Fatalf("error reported %d times, expected at most 1", errorCount)
	}
}

// --- Cleanup with e.Fail / e.Error / e.Errorf ---

func TestCleanup_CallsFail(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Cleanup(func() {
		child.Fail()
	})
	child.Close()
	e.Close()

	if !m.failed {
		t.Fatal("T should be failed after cleanup called Fail()")
	}
}

func TestCleanup_CallsError(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Cleanup(func() {
		child.Error("cleanup error")
	})
	child.Close()
	e.Close()

	if !m.failed {
		t.Fatal("T should be failed after cleanup called Error()")
	}

	found := false
	for _, msg := range m.errors {
		if msg == "cleanup error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cleanup error message should reach T, got errors: %v", m.errors)
	}
}

func TestCleanup_CallsErrorf(t *testing.T) {
	e, m := newTestE(t)

	child := e.Scope()
	child.Cleanup(func() {
		child.Errorf("cleanup %s %d", "err", 42)
	})
	child.Close()
	e.Close()

	if !m.failed {
		t.Fatal("T should be failed after cleanup called Errorf()")
	}

	found := false
	for _, msg := range m.errors {
		if msg == "cleanup err 42" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cleanup Errorf message should reach T formatted, got errors: %v", m.errors)
	}
}

// --- Cleanup panic recovery ---

func TestCleanup_PanicRecovered(t *testing.T) {
	e, _ := newTestE(t)

	ranAfterPanic := false
	child := e.Scope()
	child.Cleanup(func() { ranAfterPanic = true })
	child.Cleanup(func() { panic("boom") })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected re-panic from Close")
		}
		if r != "boom" {
			t.Fatalf("expected panic value 'boom', got %v", r)
		}
		if !ranAfterPanic {
			t.Fatal("cleanup after panicking one should still run")
		}
	}()
	child.Close()
}

// runtime.Goexit in a cleanup is handled by the recursion+defer pattern in
// runCleanups (matching testing.T.runCleanup): the deferred function processes
// remaining cleanups before Goexit continues unwinding. This is not directly
// testable from within the testing framework because Goexit propagates out of
// Close() into testing.tRunner, which treats unexpected Goexit as a failure.

// --- Run tests ---

func TestRun_ChildSharesOptions(t *testing.T) {
	e, _ := newTestE(t)

	e.Run("sub", func(child envtesting.E) {
		var cfg TestConfig
		child.Options(&cfg)
		if cfg.Key != "value" {
			t.Errorf("child should share parent's config sections, got key=%q", cfg.Key)
		}
	})
}

func TestRun_ChildHasIndependentScope(t *testing.T) {
	e, _ := newTestE(t)

	parentCleaned := false
	e.Cleanup(func() { parentCleaned = true })

	e.Run("sub", func(child envtesting.E) {
		childCleaned := false
		child.Cleanup(func() { childCleaned = true })
		if childCleaned {
			t.Error("child cleanup should NOT run before subtest ends")
		}
	})

	if parentCleaned {
		t.Error("parent cleanup should NOT run from child's subtest")
	}
}

// --- Auto-close via parent cleanup ---

func TestScope_AutoCloseOnParentCleanup(t *testing.T) {
	e, _ := newTestE(t)

	child := e.Scope()
	childCleaned := false
	child.Cleanup(func() { childCleaned = true })

	e.Close()

	if !childCleaned {
		t.Fatal("child should auto-close when parent's cleanups run")
	}
}
