package flow_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

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
	out := flow.RequeueAfter(1 * time.Second)
	if !out.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := out.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestMerge_DoneWinsOverContinue(t *testing.T) {
	out := flow.Merge(flow.Done(), flow.Continue())
	if !out.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	if out.Error() != nil {
		t.Fatalf("expected Error() == nil, got %v", out.Error())
	}
}

func TestMerge_RequeueAfterChoosesSmallest(t *testing.T) {
	out := flow.Merge(flow.RequeueAfter(5*time.Second), flow.RequeueAfter(1*time.Second))
	if !out.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}
	res, err := out.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if res.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, res.RequeueAfter)
	}
}

func TestMerge_ContinueErrAndDoneBecomesFail(t *testing.T) {
	e := errors.New("e")
	out := flow.Merge(flow.ContinueErr(e), flow.Done())
	if !out.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	_, err := out.ToCtrl()
	if err == nil {
		t.Fatalf("expected err to be non-nil")
	}
	if !errors.Is(err, e) {
		t.Fatalf("expected errors.Is(err, e) == true; err=%v", err)
	}
}

func TestMerge_ContinueErrOnlyStaysContinueErr(t *testing.T) {
	e := errors.New("e")
	out := flow.Merge(flow.ContinueErr(e))
	if out.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == false")
	}

	res, err := out.ToCtrl()
	if err == nil {
		t.Fatalf("expected err to be non-nil")
	}
	if res != (ctrl.Result{}) {
		t.Fatalf("expected empty result, got %+v", res)
	}
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

	out := flow.Continue().ReportChanged().RequireOptimisticLock()
	if !out.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == true after ReportChanged().RequireOptimisticLock()")
	}
}

func TestOutcome_Error(t *testing.T) {
	if flow.Continue().Error() != nil {
		t.Fatalf("expected Error() == nil for Continue()")
	}

	e := errors.New("e")
	if got := flow.ContinueErr(e).Error(); got == nil || !errors.Is(got, e) {
		t.Fatalf("expected Error() to contain %v, got %v", e, got)
	}
}

func TestOutcome_RequireOptimisticLock_PanicsWithoutChangeReported(t *testing.T) {
	mustPanic(t, func() { _ = flow.Continue().RequireOptimisticLock() })
}

func TestOutcome_RequireOptimisticLock_DoesNotPanicAfterReportChangedIfFalse(t *testing.T) {
	mustNotPanic(t, func() { _ = flow.Continue().ReportChangedIf(false).RequireOptimisticLock() })

	out := flow.Continue().ReportChangedIf(false).RequireOptimisticLock()
	if out.OptimisticLockRequired() {
		t.Fatalf("expected OptimisticLockRequired() == false when no change was reported")
	}
	if out.DidChange() {
		t.Fatalf("expected DidChange() == false when no change was reported")
	}
}

func TestMerge_ChangeTracking_DidChange(t *testing.T) {
	out := flow.Merge(flow.Continue(), flow.Continue().ReportChanged())
	if !out.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if out.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to not require optimistic lock")
	}
}

func TestMerge_ChangeTracking_OptimisticLockRequired(t *testing.T) {
	out := flow.Merge(
		flow.Continue().ReportChanged(),
		flow.Continue().ReportChanged().RequireOptimisticLock(),
	)
	if !out.DidChange() {
		t.Fatalf("expected merged outcome to report DidChange() == true")
	}
	if !out.OptimisticLockRequired() {
		t.Fatalf("expected merged outcome to require optimistic lock")
	}
}

func TestMerge_ChangeTracking_ChangeReportedOr(t *testing.T) {
	merged := flow.Merge(flow.Continue(), flow.Continue().ReportChangedIf(false))

	// ReportChangedIf(false) does not report a semantic change, but it does report that change tracking was used.
	if merged.DidChange() {
		t.Fatalf("expected merged outcome DidChange() == false")
	}

	// This call should not panic because Merge ORs the changeReported flag, even if no semantic change happened.
	mustNotPanic(t, func() { _ = merged.RequireOptimisticLock() })

	out := merged.RequireOptimisticLock()
	if out.OptimisticLockRequired() {
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
