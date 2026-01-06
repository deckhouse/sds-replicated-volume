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

	"github.com/deckhouse/sds-replicated-volume/internal/reconciliation/flow"
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

func TestFail_NilPanics(t *testing.T) {
	mustPanic(t, func() { _ = flow.Fail(nil) })
}

func TestRequeueAfter_ZeroPanics(t *testing.T) {
	mustPanic(t, func() { _ = flow.RequeueAfter(0) })
}

func TestRequeueAfter_NegativePanics(t *testing.T) {
	mustPanic(t, func() { _ = flow.RequeueAfter(-1 * time.Second) })
}

func TestRequeueAfter_Positive(t *testing.T) {
	outcome := flow.RequeueAfter(1 * time.Second)
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

func TestMerge_DoneWinsOverContinue(t *testing.T) {
	outcome := flow.Merge(flow.Done(), flow.Continue())
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	if outcome.Error() != nil {
		t.Fatalf("expected Error() == nil, got %v", outcome.Error())
	}
}

func TestMerge_RequeueAfterChoosesSmallest(t *testing.T) {
	outcome := flow.Merge(flow.RequeueAfter(5*time.Second), flow.RequeueAfter(1*time.Second))
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

func TestMerge_FailAndDoneBecomesFail(t *testing.T) {
	e := errors.New("e")
	outcome := flow.Merge(flow.Fail(e), flow.Done())
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

func TestMerge_FailOnlyStaysFail(t *testing.T) {
	e := errors.New("e")
	outcome := flow.Merge(flow.Fail(e))
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	_, err := outcome.ToCtrl()
	if !errors.Is(err, e) {
		t.Fatalf("expected errors.Is(err, e) == true; err=%v", err)
	}
}

func TestOutcome_DidChange(t *testing.T) {
	if flow.Continue().DidChange() {
		t.Fatalf("expected DidChange() == false for Continue()")
	}
	if !flow.Continue().ReportChanged().DidChange() {
		t.Fatalf("expected DidChange() == true after ReportChanged()")
	}
	if flow.Continue().ReportChangedIf(false).DidChange() {
		t.Fatalf("expected DidChange() == false for ReportChangedIf(false)")
	}
}

func TestOutcome_OptimisticLockRequired(t *testing.T) {
	if flow.Continue().OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false for Continue()")
	}

	if flow.Continue().ReportChanged().OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false after ReportChanged()")
	}

	outcome := flow.Continue().ReportChanged().RequireOptimisticLock()
	if !outcome.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == true after ReportChanged().RequireOptimisticLock()")
	}
}

func TestOutcome_Error(t *testing.T) {
	if flow.Continue().Error() != nil {
		t.Fatalf("expected Error() == nil for Continue()")
	}

	e := errors.New("e")
	if got := flow.Fail(e).Error(); got == nil || !errors.Is(got, e) {
		t.Fatalf("expected Error() to contain %v, got %v", e, got)
	}
}

func TestOutcome_Enrichf_IsNoOpWhenNil(t *testing.T) {
	outcome := flow.Continue().Enrichf("hello %s %d", "a", 1)
	if outcome.Error() != nil {
		t.Fatalf("expected Error() to stay nil, got %v", outcome.Error())
	}
}

func TestOutcome_Enrichf_WrapsExistingError(t *testing.T) {
	base := errors.New("base")

	outcome := flow.Fail(base).Enrichf("ctx %s", "x")
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

func TestOutcome_Enrichf_DoesNotAlterReturnDecision(t *testing.T) {
	outcome := flow.RequeueAfter(1 * time.Second).Enrichf("x")
	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	res, _ := outcome.MustToCtrl()
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be preserved, got %v", res.RequeueAfter)
	}
}

func TestOutcome_RequireOptimisticLock_PanicsWithoutChangeReported(t *testing.T) {
	mustPanic(t, func() { _ = flow.Continue().RequireOptimisticLock() })
}

func TestOutcome_RequireOptimisticLock_DoesNotPanicAfterReportChangedIfFalse(t *testing.T) {
	mustNotPanic(t, func() { _ = flow.Continue().ReportChangedIf(false).RequireOptimisticLock() })

	outcome := flow.Continue().ReportChangedIf(false).RequireOptimisticLock()
	if outcome.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false when no change was reported")
	}
	if outcome.DidChange() {
		t.Fatalf("expected DidChange() == false when no change was reported")
	}
}

func TestMerge_ChangeTracking_DidChange(t *testing.T) {
	outcome := flow.Merge(flow.Continue(), flow.Continue().ReportChanged())
	if !outcome.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if outcome.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to not require optimistic lock")
	}
}

func TestMerge_ChangeTracking_OptimisticLockRequired(t *testing.T) {
	outcome := flow.Merge(
		flow.Continue().ReportChanged(),
		flow.Continue().ReportChanged().RequireOptimisticLock(),
	)
	if !outcome.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if !outcome.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to require optimistic lock")
	}
}

func TestMerge_ChangeTracking_ChangeReportedOr(t *testing.T) {
	outcome := flow.Merge(flow.Continue(), flow.Continue().ReportChangedIf(false))

	// ReportChangedIf(false) does not report a semantic change, but it does report that change tracking was used.
	if outcome.DidChange() {
		t.Fatalf("expected merged outcome DidChange() == false")
	}

	// This call should not panic because Merge ORs the changeReported flag, even if no semantic change happened.
	mustNotPanic(t, func() { _ = outcome.RequireOptimisticLock() })

	outcome = outcome.RequireOptimisticLock()
	if outcome.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false when no change was reported")
	}
}

func TestMustBeValidPhaseName_Valid(t *testing.T) {
	valid := []string{
		"a",
		"a/b",
		"a-b.c_d",
		"A1/B2",
	}
	for _, name := range valid {
		name := name
		t.Run(name, func(t *testing.T) {
			mustNotPanic(t, func() { _, _ = flow.BeginPhase(context.Background(), name) })
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
		name := name
		t.Run(strings.ReplaceAll(name, "\t", "\\t"), func(t *testing.T) {
			mustPanic(t, func() { _, _ = flow.BeginPhase(context.Background(), name) })
		})
	}
}

func TestBeginPhase_KVOddLengthPanics(t *testing.T) {
	mustPanic(t, func() { _, _ = flow.BeginPhase(context.Background(), "p", "k") })
}

func TestBeginPhase_NestedKVInheritsAndOverrides(t *testing.T) {
	ctx, _ := flow.BeginPhase(context.Background(), "parent", "a", "1", "b", "2")
	ctx, _ = flow.BeginPhase(ctx, "child", "b", "3", "c", "4")

	outcome := flow.Failf(errors.New("e"), "step")
	flow.EndPhase(ctx, &outcome)

	if outcome.Error() == nil {
		t.Fatalf("expected error to be non-nil")
	}

	s := outcome.Error().Error()
	if !strings.Contains(s, "step") {
		t.Fatalf("expected error to contain local context; got %q", s)
	}
}

func TestEndPhase_LogsFailAsError_OnceAndMarksLogged(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	ctx, _ = flow.BeginPhase(ctx, "p")

	outcome := flow.Failf(errors.New("e"), "step")
	flow.EndPhase(ctx, &outcome)

	if !outcome.ErrorLogged() {
		t.Fatalf("expected ErrorLogged() == true")
	}

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

func TestEndPhase_NestedPhases_DoNotDoubleLogSameError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	zl := zap.New(core)
	l := zapr.NewLogger(zl)

	ctx := log.IntoContext(context.Background(), l)
	parentCtx, _ := flow.BeginPhase(ctx, "parent")
	childCtx, _ := flow.BeginPhase(parentCtx, "child")

	outcome := flow.Failf(errors.New("e"), "step")
	flow.EndPhase(childCtx, &outcome)
	flow.EndPhase(parentCtx, &outcome)

	// Only the first EndPhase should emit an Error-level "phase end" with error details.
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
