package flow

import (
	"errors"
	"strings"
	"testing"
	"time"
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
	if got := Wrapf(nil, "x %d", 1); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestWrapf_Unwrap(t *testing.T) {
	base := errors.New("base")
	wrapped := Wrapf(base, "x")
	if !errors.Is(wrapped, base) {
		t.Fatalf("expected errors.Is(wrapped, base) == true; wrapped=%v", wrapped)
	}
}

func TestWrapf_Formatting(t *testing.T) {
	base := errors.New("base")
	wrapped := Wrapf(base, "hello %s %d", "a", 1)

	s := wrapped.Error()
	if !strings.Contains(s, "hello a 1") {
		t.Fatalf("expected wrapped error string to contain formatted prefix; got %q", s)
	}
	if !strings.Contains(s, base.Error()) {
		t.Fatalf("expected wrapped error string to contain base error string; got %q", s)
	}
}

func TestFail_NilPanics(t *testing.T) {
	mustPanic(t, func() { _ = Fail(nil) })
}

func TestRequeueAfter_ZeroPanics(t *testing.T) {
	mustPanic(t, func() { _ = RequeueAfter(0) })
}

func TestRequeueAfter_NegativePanics(t *testing.T) {
	mustPanic(t, func() { _ = RequeueAfter(-1 * time.Second) })
}

func TestRequeueAfter_Positive(t *testing.T) {
	out := RequeueAfter(1 * time.Second)
	if out.Return == nil {
		t.Fatalf("expected Return to be non-nil")
	}
	if out.Return.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, out.Return.RequeueAfter)
	}
}

func TestMerge_DoneWinsOverContinue(t *testing.T) {
	out := Merge(Done(), Continue())
	if out.Return == nil {
		t.Fatalf("expected Return to be non-nil")
	}
	if out.Err != nil {
		t.Fatalf("expected Err to be nil, got %v", out.Err)
	}
}

func TestMerge_RequeueAfterChoosesSmallest(t *testing.T) {
	out := Merge(RequeueAfter(5*time.Second), RequeueAfter(1*time.Second))
	if out.Return == nil {
		t.Fatalf("expected Return to be non-nil")
	}
	if out.Return.RequeueAfter != 1*time.Second {
		t.Fatalf("expected RequeueAfter to be %v, got %v", 1*time.Second, out.Return.RequeueAfter)
	}
	if out.Err != nil {
		t.Fatalf("expected Err to be nil, got %v", out.Err)
	}
}

func TestMerge_ContinueErrAndDoneBecomesFail(t *testing.T) {
	e := errors.New("e")
	out := Merge(ContinueErr(e), Done())
	if out.Return == nil {
		t.Fatalf("expected Return to be non-nil")
	}
	if out.Err == nil {
		t.Fatalf("expected Err to be non-nil")
	}
	if !errors.Is(out.Err, e) {
		t.Fatalf("expected errors.Is(out.Err, e) == true; out.Err=%v", out.Err)
	}
}

func TestMerge_ContinueErrOnlyStaysContinueErr(t *testing.T) {
	e := errors.New("e")
	out := Merge(ContinueErr(e))
	if out.Return != nil {
		t.Fatalf("expected Return to be nil")
	}
	if out.Err == nil {
		t.Fatalf("expected Err to be non-nil")
	}
	if !errors.Is(out.Err, e) {
		t.Fatalf("expected errors.Is(out.Err, e) == true; out.Err=%v", out.Err)
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
			mustNotPanic(t, func() { mustBeValidPhaseName(name) })
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
			mustPanic(t, func() { mustBeValidPhaseName(name) })
		})
	}
}
