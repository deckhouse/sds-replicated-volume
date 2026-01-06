package flow

import (
	"context"
	"errors"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func mustPanicInternal(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	fn()
}

func TestMerge_RequeueTruePanics_InternalGuard(t *testing.T) {
	// This is an internal guard: ctrl.Result{Requeue:true} is not constructible via flow's public API.
	// We keep this test to ensure Merge keeps rejecting the unsupported Requeue=true mode.
	mustPanicInternal(t, func() {
		_ = Merge(Outcome{result: &ctrl.Result{Requeue: true}})
	})
}

func TestOutcome_ErrWithoutResult_IsClassifiedAsInvalidKind(t *testing.T) {
	kind, _ := outcomeKind(&Outcome{err: errors.New("e")})
	if kind != "invalid" {
		t.Fatalf("expected kind=invalid, got %q", kind)
	}
}

func TestEndPhase_ErrWithoutResult_DoesNotPanic(t *testing.T) {
	ctx, _ := BeginPhase(context.Background(), "p")
	o := Outcome{err: errors.New("e")}
	EndPhase(ctx, &o)
}
