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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
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

func TestReconcileFlow_DoneAndRequeueAfter_ZeroPanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.DoneAndRequeueAfter(0) })
}

func TestReconcileFlow_DoneAndRequeueAfter_NegativePanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.DoneAndRequeueAfter(-1 * time.Second) })
}

func TestReconcileFlow_DoneAndRequeueAfter_Positive(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.DoneAndRequeueAfter(1 * time.Second)
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true for DoneAndRequeueAfter")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestReconcileFlow_DoneAndRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.DoneAndRequeue()
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true for DoneAndRequeue")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue to be true")
	}
}

func TestReconcileFlow_ContinueAndRequeueAfter_ZeroPanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.ContinueAndRequeueAfter(0) })
}

func TestReconcileFlow_ContinueAndRequeueAfter_NegativePanics(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	mustPanic(t, func() { _ = rf.ContinueAndRequeueAfter(-1 * time.Second) })
}

func TestReconcileFlow_ContinueAndRequeueAfter_Positive(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.ContinueAndRequeueAfter(1 * time.Second)
	if outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == false for ContinueAndRequeueAfter")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestReconcileFlow_ContinueAndRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.ContinueAndRequeue()
	if outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == false for ContinueAndRequeue")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue to be true")
	}
}

func TestMergeReconciles_DoneWinsOverContinue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := flow.MergeReconciles(rf.Done(), rf.Continue())
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	if outcome.Error() != nil {
		t.Fatalf("expected Error() == nil, got %v", outcome.Error())
	}
}

func TestMergeReconciles_DoneAndRequeueAfterChoosesSmallest(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := flow.MergeReconciles(rf.DoneAndRequeueAfter(5*time.Second), rf.DoneAndRequeueAfter(1*time.Second))
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

func TestMergeReconciles_ContinueAndRequeueAfterChoosesSmallest(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := flow.MergeReconciles(rf.ContinueAndRequeueAfter(5*time.Second), rf.ContinueAndRequeueAfter(1*time.Second))
	if outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == false for merged ContinueAndRequeueAfter")
	}
	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestMergeReconciles_TerminalWinsOverNonTerminal(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	// Terminal (Done) should win over non-terminal (ContinueAndRequeueAfter)
	outcome := flow.MergeReconciles(rf.Done(), rf.ContinueAndRequeueAfter(1*time.Second))
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true (terminal wins)")
	}
	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	// Done without requeue intent
	if res.Requeue || res.RequeueAfter != 0 { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected no requeue for Done, got Requeue=%v RequeueAfter=%v", res.Requeue, res.RequeueAfter) //nolint:staticcheck // testing Requeue field
	}
}

func TestMergeReconciles_TerminalRequeueWinsOverTerminalDone(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	// DoneAndRequeue should win over Done
	outcome := flow.MergeReconciles(rf.Done(), rf.DoneAndRequeue())
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue to be true (requeue wins over done)")
	}
}

func TestMergeReconciles_FailAndDoneBecomesFail(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	e := errors.New("e")
	outcome := flow.MergeReconciles(rf.Fail(e), rf.Done())
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

func TestMergeReconciles_FailOnlyStaysFail(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	e := errors.New("e")
	outcome := flow.MergeReconciles(rf.Fail(e))
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
	outcome := rf.DoneAndRequeueAfter(1 * time.Second).Enrichf("x")
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

func TestReconcileOutcome_MustToCtrl_DoesNotPanicOnContinueWithRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	// ContinueAndRequeue has requeue intent, so MustToCtrl should not panic
	mustNotPanic(t, func() { _, _ = rf.ContinueAndRequeue().MustToCtrl() })
	mustNotPanic(t, func() { _, _ = rf.ContinueAndRequeueAfter(1 * time.Second).MustToCtrl() })
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

func TestMergeEnsures_ChangeTracking_DidChange(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	o := flow.MergeEnsures(ef.Ok(), ef.Ok().ReportChanged())
	if !o.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
}

func TestMergeEnsures_ChangeTracking_AllUnchanged(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	o := flow.MergeEnsures(ef.Ok(), ef.Ok().ReportChangedIf(false))
	if o.DidChange() {
		t.Fatalf("expected merged outcome DidChange() == false")
	}
}

func TestMergeEnsures_ErrorsJoined(t *testing.T) {
	ef := flow.BeginEnsure(context.Background(), "test")
	var outcome flow.EnsureOutcome
	defer ef.OnEnd(&outcome)

	e1 := errors.New("e1")
	e2 := errors.New("e2")
	o := flow.MergeEnsures(ef.Err(e1), ef.Err(e2))

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

func TestMergeSteps(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e1 := errors.New("e1")
	e2 := errors.New("e2")
	got := flow.MergeSteps(e1, e2)

	if got == nil {
		t.Fatalf("expected MergeSteps() to return non-nil")
	}
	if !errors.Is(got, e1) {
		t.Fatalf("expected errors.Is(got, e1) == true; got=%v", got)
	}
	if !errors.Is(got, e2) {
		t.Fatalf("expected errors.Is(got, e2) == true; got=%v", got)
	}
}

func TestMergeSteps_AllNil(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	got := flow.MergeSteps(nil, nil)
	if got != nil {
		t.Fatalf("expected MergeSteps(nil, nil) to return nil, got %v", got)
	}
}

func TestMergeSteps_SomeNil(t *testing.T) {
	sf := flow.BeginStep(context.Background(), "test")
	var err error
	defer sf.OnEnd(&err)

	e := errors.New("e")
	got := flow.MergeSteps(nil, e, nil)

	if got == nil {
		t.Fatalf("expected MergeSteps() to return non-nil")
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
	if got := m["result"]; got != "Fail" {
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

	outcome := ef.Ok().ReportChanged()
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

// =============================================================================
// Conflict (optimistic lock) handling tests
// =============================================================================

func newConflictError() error {
	return apierrors.NewConflict(schema.GroupResource{Resource: "test"}, "obj", errors.New("optimistic lock"))
}

func TestToCtrl_ConflictErrorBecomesRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Fail(newConflictError())

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected nil error from ToCtrl for conflict, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue=true for conflict, got %v", res)
	}
}

func TestToCtrl_WrappedConflictErrorBecomesRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	// Simulate: rf.Fail(err).Enrichf("patching RSP")
	outcome := rf.Fail(newConflictError()).Enrichf("patching RSP")

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected nil error from ToCtrl for wrapped conflict, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue=true for wrapped conflict, got %v", res)
	}
}

func TestToCtrl_FailfConflictBecomesRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Failf(newConflictError(), "patching RSP")

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected nil error from ToCtrl for Failf conflict, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue=true for Failf conflict, got %v", res)
	}
}

func TestToCtrl_NonConflictErrorPassedThrough(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	e := errors.New("real error")
	outcome := rf.Fail(e)

	_, err := outcome.ToCtrl()
	if !errors.Is(err, e) {
		t.Fatalf("expected non-conflict error to pass through, got %v", err)
	}
}

func TestMustToCtrl_ConflictErrorBecomesRequeue(t *testing.T) {
	rf := flow.BeginRootReconcile(context.Background())
	outcome := rf.Fail(newConflictError())

	res, err := outcome.MustToCtrl()
	if err != nil {
		t.Fatalf("expected nil error from MustToCtrl for conflict, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing Requeue field
		t.Fatalf("expected Requeue=true for conflict, got %v", res)
	}
}

func TestReconcileFlow_OnEnd_ConflictLoggedAtInfoNotError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	rf := flow.BeginReconcile(ctx, "p")

	outcome := rf.Fail(newConflictError())
	rf.OnEnd(&outcome)

	// Should have zero Error-level "phase end" entries.
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			t.Fatalf("conflict should not be logged at Error level; got %v", e)
		}
	}

	// Should have exactly one Info-level "phase end" entry (V(0) = Info).
	var infoMatches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.InfoLevel {
			infoMatches = append(infoMatches, e)
		}
	}
	if len(infoMatches) != 1 {
		t.Fatalf("expected exactly 1 info 'phase end' log entry, got %d; entries=%v", len(infoMatches), observed.All())
	}

	m := infoMatches[0].ContextMap()
	if got := m["result"]; got != "Conflict" {
		t.Fatalf("expected result=Conflict, got %v", got)
	}
	if got := m["hasError"]; got != true {
		t.Fatalf("expected hasError=true, got %v", got)
	}
}

func TestReconcileFlow_OnEnd_WrappedConflictLoggedAtInfoNotError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	rf := flow.BeginReconcile(ctx, "p")

	outcome := rf.Failf(newConflictError(), "patching RSP")
	rf.OnEnd(&outcome)

	// No Error-level logs for conflict.
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			t.Fatalf("wrapped conflict should not be logged at Error level; got %v", e)
		}
	}

	// Should have an Info-level log with result=Conflict.
	var infoMatches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.InfoLevel {
			infoMatches = append(infoMatches, e)
		}
	}
	if len(infoMatches) != 1 {
		t.Fatalf("expected exactly 1 info 'phase end' log entry, got %d; entries=%v", len(infoMatches), observed.All())
	}
	if got := infoMatches[0].ContextMap()["result"]; got != "Conflict" {
		t.Fatalf("expected result=Conflict, got %v", got)
	}
}

func TestReconcileFlow_OnEnd_NestedPhases_ConflictLoggedOnce(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	parentRf := flow.BeginReconcile(ctx, "parent")
	childRf := flow.BeginReconcile(parentRf.Ctx(), "child")

	outcome := childRf.Fail(newConflictError())
	childRf.OnEnd(&outcome)
	parentRf.OnEnd(&outcome)

	// No Error-level logs at all.
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			t.Fatalf("conflict should not be logged at Error level in nested phases; got %v", e)
		}
	}

	// Exactly one Info-level "phase end" (from child), one Debug-level (from parent, already logged).
	infoCount := 0
	debugCount := 0
	for _, e := range observed.All() {
		if e.Message == "phase end" {
			switch e.Level {
			case zapcore.InfoLevel:
				infoCount++
			case zapcore.DebugLevel:
				debugCount++
			}
		}
	}
	if infoCount != 1 {
		t.Fatalf("expected exactly 1 info 'phase end' log entry, got %d", infoCount)
	}
	if debugCount != 1 {
		t.Fatalf("expected exactly 1 debug 'phase end' log entry (parent after conflict already logged), got %d", debugCount)
	}
}

func TestReconcileFlow_OnEnd_NonConflictStillLoggedAtError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	rf := flow.BeginReconcile(ctx, "p")

	outcome := rf.Fail(errors.New("real error"))
	rf.OnEnd(&outcome)

	// Should still log at Error level.
	var errorMatches []observer.LoggedEntry
	for _, e := range observed.All() {
		if e.Message == "phase end" && e.Level == zapcore.ErrorLevel {
			errorMatches = append(errorMatches, e)
		}
	}
	if len(errorMatches) != 1 {
		t.Fatalf("expected exactly 1 error 'phase end' log entry for non-conflict, got %d; entries=%v",
			len(errorMatches), observed.All())
	}
	if got := errorMatches[0].ContextMap()["result"]; got != "Fail" {
		t.Fatalf("expected result=Fail, got %v", got)
	}
}
