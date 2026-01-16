package flow

import (
	"context"
	"errors"
	"testing"
)

func TestReconcileOutcome_ErrWithoutResult_IsClassifiedAsInvalidKind(t *testing.T) {
	kind, _ := reconcileOutcomeKind(&ReconcileOutcome{err: errors.New("e")})
	if kind != "invalid" {
		t.Fatalf("expected kind=invalid, got %q", kind)
	}
}

func TestReconcileFlow_OnEnd_ErrWithoutResult_DoesNotPanic(t *testing.T) {
	rf := BeginReconcile(context.Background(), "p")
	o := ReconcileOutcome{err: errors.New("e")}
	rf.OnEnd(&o)
}

func TestReconcileFlow_Merge_RequeueIsSupported(t *testing.T) {
	rf := BeginRootReconcile(context.Background())
	outcome := rf.Merge(rf.Requeue(), rf.Continue())

	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue {
		t.Fatalf("expected Requeue to be true")
	}
}

func TestReconcileFlow_Merge_RequeueWinsOverRequeueAfter(t *testing.T) {
	rf := BeginRootReconcile(context.Background())
	// Requeue() = delay 0, RequeueAfter(5) = delay 5.
	// Minimum delay wins, so Requeue() wins.
	outcome := rf.Merge(rf.Requeue(), rf.RequeueAfter(5))

	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue {
		t.Fatalf("expected Requeue to be true (delay=0 wins)")
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected RequeueAfter to be 0 when Requeue is set, got %v", res.RequeueAfter)
	}
}
