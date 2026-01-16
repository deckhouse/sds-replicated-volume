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

package flow_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/flow"
)

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	fn()
}

func mustNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}

// =============================================================================
// Wrapf tests
// =============================================================================

func TestWrapf_NilError(t *testing.T) {
	if got := flow.Wrapf(nil, "x %d", 1); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestWrapf_Unwrap(t *testing.T) {
	base := errors.New("base")
	wrapped := flow.Wrapf(base, "x")
	if !errors.Is(wrapped, base) {
		t.Fatalf("expected errors.Is(wrapped, base) == true; wrapped=%v", wrapped)
	}
}

func TestWrapf_Formatting(t *testing.T) {
	base := errors.New("base")
	wrapped := flow.Wrapf(base, "hello %s %d", "a", 1)

	s := wrapped.Error()
	if !strings.Contains(s, "hello a 1") {
		t.Fatalf("expected wrapped error string to contain formatted prefix; got %q", s)
	}
	if !strings.Contains(s, base.Error()) {
		t.Fatalf("expected wrapped error string to contain base error string; got %q", s)
	}
}

// =============================================================================
// ReconcileFlow and ReconcileOutcome tests
// =============================================================================

func TestReconcileFlow_Fail_NilPanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.Fail(nil) })
}

func TestReconcileFlow_RequeueAfter_ZeroPanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.RequeueAfter(0) })
}

func TestReconcileFlow_RequeueAfter_NegativePanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.RequeueAfter(-1 * time.Second) })
}

func TestReconcileFlow_RequeueAfter_Positive(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.RequeueAfter(1 * time.Second)
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestReconcileFlow_Requeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Requeue()
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing deprecated Requeue field
		t.Fatalf("expected Requeue to be true")
	}
}

func TestReconcileFlow_Merge_DoneWinsOverContinue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Merge(rf.Done(), rf.Continue())
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	if outcome.Error() != nil {
		t.Fatalf("expected Error() == nil, got %v", outcome.Error())
	}
}

func TestReconcileFlow_Merge_RequeueAfterChoosesSmallest(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Merge(rf.RequeueAfter(5*time.Second), rf.RequeueAfter(1*time.Second))
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestReconcileFlow_Merge_FailAndDoneBecomesFail(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	e := errors.New("e")
	outcome := rf.Merge(rf.Fail(e), rf.Done())
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	_, err := outcome.ToCtrl()
	if err == nil {
		t.Fatalf("expected err to be non-nil")
	}
	if !errors.Is(err, e) {
		t.Fatalf("expected errors.Is(err, e) == true; err=%v", err)
	}
}

func TestReconcileFlow_Merge_FailOnlyStaysFail(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	e := errors.New("e")
	outcome := rf.Merge(rf.Fail(e))
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	_, err := outcome.ToCtrl()
	if !errors.Is(err, e) {
		t.Fatalf("expected errors.Is(err, e) == true; err=%v", err)
	}
}

func TestReconcileOutcome_Error(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	if rf.Continue().Error() != nil {
		t.Fatalf("expected Error() == nil for Continue()")
	}

	e := errors.New("e")
	if got := rf.Fail(e).Error(); got == nil || !errors.Is(got, e) {
		t.Fatalf("expected Error() to contain %v, got %v", e, got)
	}
}

func TestReconcileOutcome_Enrichf_IsNoOpWhenNil(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Continue().Enrichf("hello %s %d", "a", 1)
	if outcome.Error() != nil {
		t.Fatalf("expected Error() to stay nil, got %v", outcome.Error())
	}
}

func TestReconcileOutcome_Enrichf_WrapsExistingError(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	base := errors.New("base")

	outcome := rf.Fail(base).Enrichf("ctx %s", "x")
	if outcome.Error() == nil {
		t.Fatalf("expected Error() to be non-nil")
	}
	if !errors.Is(outcome.Error(), base) {
		t.Fatalf("expected errors.Is(outcome.Error(), base) == true; err=%v", outcome.Error())
	}
	if got := outcome.Error().Error(); !strings.Contains(got, "ctx x") {
		t.Fatalf("expected wrapped error to contain formatted prefix; got %q", got)
	}
}

func TestReconcileOutcome_Enrichf_DoesNotAlterReturnDecision(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.RequeueAfter(1 * time.Second).Enrichf("x")
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	res, _ := outcome.MustToCtrl()
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be preserved, got %v", res.RequeueAfter)
	}
}

func TestReconcileOutcome_MustToCtrl_PanicsOnContinue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _, _ = rf.Continue().MustToCtrl() })
}

// =============================================================================
// EnsureFlow and EnsureOutcome tests
// =============================================================================

func TestEnsureOutcome_DidChange(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	if ef.Ok().DidChange() {
		t.Fatalf("expected DidChange() == false for Ok()")
	}
	if !ef.Ok().ReportChanged().DidChange() {
		t.Fatalf("expected DidChange() == true after ReportChanged()")
	}
	if ef.Ok().ReportChangedIf(false).DidChange() {
		t.Fatalf("expected DidChange() == false for ReportChangedIf(false)")
	}
}

func TestEnsureOutcome_OptimisticLockRequired(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	if ef.Ok().OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false for Ok()")
	}

	if ef.Ok().ReportChanged().OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false after ReportChanged()")
	}

	o := ef.Ok().ReportChanged().RequireOptimisticLock()
	if !o.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == true after ReportChanged().RequireOptimisticLock()")
	}
}

func TestEnsureOutcome_RequireOptimisticLock_PanicsWithoutChangeReported(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	mustPanic(t, func() { _ = ef.Ok().RequireOptimisticLock() })
}

func TestEnsureOutcome_RequireOptimisticLock_DoesNotPanicAfterReportChangedIfFalse(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	mustNotPanic(t, func() { _ = ef.Ok().ReportChangedIf(false).RequireOptimisticLock() })

	o := ef.Ok().ReportChangedIf(false).RequireOptimisticLock()
	if o.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false when no change was reported")
	}
	if o.DidChange() {
		t.Fatalf("expected DidChange() == false when no change was reported")
	}
}

func TestEnsureOutcome_Error(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	if ef.Ok().Error() != nil {
		t.Fatalf("expected Error() == nil for Ok()")
	}

	e := errors.New("e")
	if got := ef.Err(e).Error(); got == nil || !errors.Is(got, e) {
		t.Fatalf("expected Error() to contain %v, got %v", e, got)
	}
}

func TestEnsureFlow_Err_NilIsAllowed(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	// Unlike ReconcileFlow.Fail, EnsureFlow.Err(nil) is allowed and equivalent to Ok()
	o := ef.Err(nil)
	if o.Error() != nil {
		t.Fatalf("expected Error() == nil for Err(nil), got %v", o.Error())
	}
}

func TestEnsureOutcome_Enrichf(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	base := errors.New("base")
	o := ef.Err(base).Enrichf("ctx %s", "x")
	if o.Error() == nil {
		t.Fatalf("expected Error() to be non-nil")
	}
	if !errors.Is(o.Error(), base) {
		t.Fatalf("expected errors.Is(o.Error(), base) == true; err=%v", o.Error())
	}
	if got := o.Error().Error(); !strings.Contains(got, "ctx x") {
		t.Fatalf("expected wrapped error to contain formatted prefix; got %q", got)
	}
}

func TestEnsureFlow_Merge_ChangeTracking_DidChange(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	o := ef.Merge(ef.Ok(), ef.Ok().ReportChanged())
	if !o.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if o.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to not require optimistic lock")
	}
}

func TestEnsureFlow_Merge_ChangeTracking_OptimisticLockRequired(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	o := ef.Merge(
		ef.Ok().ReportChanged(),
		ef.Ok().ReportChanged().RequireOptimisticLock(),
	)
	if !o.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if !o.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to require optimistic lock")
	}
}

func TestEnsureFlow_Merge_ChangeTracking_ChangeReportedOr(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	o := ef.Merge(ef.Ok(), ef.Ok().ReportChangedIf(false))

	// ReportChangedIf(false) does not report a semantic change, but it does report that change tracking was used.
	if o.DidChange() {
		t.Fatalf("expected merged outcome DidChange() == false")
	}

	// This call should not panic because Merge ORs the changeReported flag, even if no semantic change happened.
	mustNotPanic(t, func() { _ = o.RequireOptimisticLock() })

	o = o.RequireOptimisticLock()
	if o.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false when no change was reported")
	}
}

func TestEnsureFlow_Merge_ErrorsJoined(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	e1 := errors.New("e1")
	e2 := errors.New("e2")
	o := ef.Merge(ef.Err(e1), ef.Err(e2))

	if o.Error() == nil {
		t.Fatalf("expected Error() to be non-nil")
	}
	if !errors.Is(o.Error(), e1) {
		t.Fatalf("expected errors.Is(o.Error(), e1) == true; err=%v", o.Error())
	}
	if !errors.Is(o.Error(), e2) {
		t.Fatalf("expected errors.Is(o.Error(), e2) == true; err=%v", o.Error())
	}
}

// =============================================================================
// StepFlow tests
// =============================================================================

func TestStepFlow_Ok(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	if sf.Ok() != nil {
		t.Fatalf("expected Ok() to return nil")
	}
}

func TestStepFlow_Err(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e := errors.New("e")
	if got := sf.Err(e); got != e {
		t.Fatalf("expected Err(e) to return e, got %v", got)
	}
}

func TestStepFlow_Errf(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	got := sf.Errf("hello %s %d", "a", 1)
	if got == nil {
		t.Fatalf("expected Errf() to return non-nil")
	}
	if !strings.Contains(got.Error(), "hello a 1") {
		t.Fatalf("expected error string to contain formatted message; got %q", got.Error())
	}
}

func TestStepFlow_Merge(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e1 := errors.New("e1")
	e2 := errors.New("e2")
	got := sf.Merge(e1, e2)

	if got == nil {
		t.Fatalf("expected Merge() to return non-nil")
	}
	if !errors.Is(got, e1) {
		t.Fatalf("expected errors.Is(got, e1) == true; got=%v", got)
	}
	if !errors.Is(got, e2) {
		t.Fatalf("expected errors.Is(got, e2) == true; got=%v", got)
	}
}

func TestStepFlow_Merge_AllNil(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	got := sf.Merge(nil, nil)
	if got != nil {
		t.Fatalf("expected Merge(nil, nil) to return nil, got %v", got)
	}
}

func TestStepFlow_Merge_SomeNil(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e := errors.New("e")
	got := sf.Merge(nil, e, nil)

	if got == nil {
		t.Fatalf("expected Merge() to return non-nil")
	}
	if !errors.Is(got, e) {
		t.Fatalf("expected errors.Is(got, e) == true; got=%v", got)
	}
}

func TestStepFlow_Err_NilPanics(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on Err(nil)")
		}
	}()

	_ = sf.Err(nil)
}

func TestStepFlow_Enrichf(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e := errors.New("original")
	got := sf.Enrichf(e, "context %d", 42)

	if got == nil {
		t.Fatalf("expected Enrichf() to return non-nil")
	}
	if !errors.Is(got, e) {
		t.Fatalf("expected errors.Is(got, e) == true; got=%v", got)
	}
	if !strings.Contains(got.Error(), "context 42") {
		t.Fatalf("expected error string to contain 'context 42'; got %q", got.Error())
	}
}

func TestStepFlow_Enrichf_NilIsNoOp(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	got := sf.Enrichf(nil, "context")
	if got != nil {
		t.Fatalf("expected Enrichf(nil, ...) to return nil, got %v", got)
	}
}

// =============================================================================
// Phase validation tests
// =============================================================================

func TestMustBeValidPhaseName_Valid(t *testing.T) {
	valid := []string{
		"a",
		"a/b",
		"a-b.c_d",
		"A1/B2",
	}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			mustNotPanic(t, func() { _ = flow.BeginReconcile(context.Background(), name) })
		})
	}
}

func TestMustBeValidPhaseName_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"/a",
		"a/",
		"a//b",
		"a b",
		"a\tb",
		"a:b",
	}
	for _, name := range invalid {
		t.Run(strings.ReplaceAll(name, "\t", "\\t"), func(t *testing.T) {
			mustPanic(t, func() { _ = flow.BeginReconcile(context.Background(), name) })
		})
	}
}

func TestBeginReconcile_KVOddLengthPanics(t *testing.T) {
	//nolint:staticcheck // testing panic for odd kv length
	mustPanic(t, func() { _ = flow.BeginReconcile(context.Background(), "p", "k") })
}

func TestBeginEnsure_KVOddLengthPanics(t *testing.T) {
	//nolint:staticcheck // testing panic for odd kv length
	mustPanic(t, func() { _ = flow.BeginEnsure(context.Background(), "p", "k") })
}

func TestBeginStep_KVOddLengthPanics(t *testing.T) {
	//nolint:staticcheck // testing panic for odd kv length
	mustPanic(t, func() { _ = flow.BeginStep(context.Background(), "p", "k") })
}

// =============================================================================
// End logging tests
// =============================================================================

func TestReconcileFlow_OnEnd_LogsFailAsError_OnceAndMarksLogged(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	rf := flow.BeginReconcile(ctx, "p")

	outcome := rf.Failf(errors.New("e"), "step")
	rf.OnEnd(&outcome)

	// Should log exactly one Error-level "phase end" record (Fail*), with summary fields.
	var matches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			matches = append(matches, e)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry, got %d; entries=%v", len(matches), observed.All())
	}

	m := matches[0].ContextMap()
	if got := m["result"]; got != "fail" {
		t.Fatalf("expected result=fail, got %v", got)
	}
	if got := m["hasError"]; got != true {
		t.Fatalf("expected hasError=true, got %v", got)
	}
	if _, ok := m["duration"]; !ok {
		t.Fatalf("expected duration to be present; got %v", m)
	}
}

func TestReconcileFlow_OnEnd_NestedPhases_DoNotDoubleLogSameError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	parentRf := flow.BeginReconcile(ctx, "parent")
	childRf := flow.BeginReconcile(parentRf.Ctx(), "child")

	outcome := childRf.Failf(errors.New("e"), "step")
	childRf.OnEnd(&outcome)
	parentRf.OnEnd(&outcome)

	// Only the first End should emit an Error-level "phase end" with error details.
	count := 0
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry, got %d; entries=%v", count, observed.All())
	}

	// Error chain should not be wrapped with phase context.
	if outcome.Error() == nil {
		t.Fatalf("expected error to be non-nil")
	}
	s := outcome.Error().Error()
	if strings.Contains(s, "phase child") || strings.Contains(s, "phase parent") {
		t.Fatalf("expected error to not contain phase wrappers; got %q", s)
	}
}

func TestEnsureFlow_OnEnd_LogsErrorAsError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	ef := flow.BeginEnsure(ctx, "ensure-test")

	outcome := ef.Err(errors.New("e"))
	ef.OnEnd(&outcome)

	var matches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			matches = append(matches, e)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry, got %d; entries=%v", len(matches), observed.All())
	}

	m := matches[0].ContextMap()
	if got := m["hasError"]; got != true {
		t.Fatalf("expected hasError=true, got %v", got)
	}
}

func TestStepFlow_OnEnd_LogsErrorAsError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	sf := flow.BeginStep(ctx, "step-test")

	err := errors.New("e")
	sf.OnEnd(&err)

	var matches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			matches = append(matches, e)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry, got %d; entries=%v", len(matches), observed.All())
	}

	m := matches[0].ContextMap()
	if got := m["hasError"]; got != true {
		t.Fatalf("expected hasError=true, got %v", got)
	}
}

func TestEnsureFlow_OnEnd_LogsChangeTrackingFields(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	ef := flow.BeginEnsure(ctx, "ensure-test")

	outcome := ef.Ok().ReportChanged().RequireOptimisticLock()
	ef.OnEnd(&outcome)

	// Find V(1) "phase end" log (no error, so Debug level in zap for V(1).Info)
	var matches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.DebugLevel {
			matches = append(matches, e)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 debug 'phase end' log entry, got %d; entries=%v", len(matches), observed.All())
	}

	m := matches[0].ContextMap()
	if got := m["changed"]; got != true {
		t.Fatalf("expected changed=true, got %v", got)
	}
	if got := m["optimisticLock"]; got != true {
		t.Fatalf("expected optimisticLock=true, got %v", got)
	}
	if got := m["hasError"]; got != false {
		t.Fatalf("expected hasError=false, got %v", got)
	}
}

func TestReconcileFlow_OnEnd_NestedPhases_SecondOnEndLogsAtDebugLevel(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	parentRf := flow.BeginReconcile(ctx, "parent")
	childRf := flow.BeginReconcile(parentRf.Ctx(), "child")

	outcome := childRf.Fail(errors.New("e"))
	childRf.OnEnd(&outcome)
	parentRf.OnEnd(&outcome)

	// Count error-level and debug-level "phase end" logs
	// (V(1).Info logs at debug level in zap)
	errorCount := 0
	debugCount := 0
	for _, e := range observed.All() {
		if e.Message == "phase end" {
			switch e.Level {
			case zapcore.ErrorLevel:
				errorCount++
			case zapcore.DebugLevel:
				debugCount++
			}
		}
	}

	// First End logs at Error level, second End logs at Debug level (V(1).Info)
	if errorCount != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry, got %d", errorCount)
	}
	if debugCount != 1 {
		t.Fatalf("expected exactly 1 debug 'phase end' log entry (for parent after error already logged), got %d", debugCount)
	}
}

func TestReconcileFlow_OnEnd_PanicIsLoggedAndReraised(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic to be re-raised")
		}
		if r != "test panic" {
			t.Fatalf("expected panic value 'test panic', got %v", r)
		}

		// Verify "phase panic" was logged
		var matches []observer.LoggedEntry
		for _, e := range observed.All() {
			if e.Message == "phase panic" && e.Level == zapcore.ErrorLevel {
				matches = append(matches, e)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("expected exactly 1 'phase panic' log entry, got %d; entries=%v", len(matches), observed.All())
		}
	}()

	rf := flow.BeginReconcile(ctx, "test")
	var outcome flow.ReconcileOutcome
	defer rf.OnEnd(&outcome)

	panic("test panic")
}

func TestEnsureFlow_OnEnd_PanicIsLoggedAndReraised(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic to be re-raised")
		}

		// Verify "phase panic" was logged
		var matches []observer.LoggedEntry
		for _, e := range observed.All() {
			if e.Message == "phase panic" && e.Level == zapcore.ErrorLevel {
				matches = append(matches, e)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("expected exactly 1 'phase panic' log entry, got %d; entries=%v", len(matches), observed.All())
		}
	}()

	ef := flow.BeginEnsure(ctx, "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	panic("test panic")
}

func TestStepFlow_OnEnd_PanicIsLoggedAndReraised(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic to be re-raised")
		}

		// Verify "phase panic" was logged
		var matches []observer.LoggedEntry
		for _, e := range observed.All() {
			if e.Message == "phase panic" && e.Level == zapcore.ErrorLevel {
				matches = append(matches, e)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("expected exactly 1 'phase panic' log entry, got %d; entries=%v", len(matches), observed.All())
		}
	}()

	sf := flow.BeginStep(ctx, "test")
	var err error
	defer sf.OnEnd(&err)

	panic("test panic")
}
