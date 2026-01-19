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

func TestReconcileFlow_OnEnd_ErrWithoutResult_DoesNotPanic(_ *testing.T) {
	rf := BeginReconcile(context.Background(), "p")
	o := ReconcileOutcome{err: errors.New("e")}
	rf.OnEnd(&o)
}

func TestMergeReconciles_RequeueIsSupported(t *testing.T) {
	rf := BeginRootReconcile(context.Background())
	outcome := MergeReconciles(rf.Requeue(), rf.Continue())

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

func TestMergeReconciles_RequeueWinsOverRequeueAfter(t *testing.T) {
	rf := BeginRootReconcile(context.Background())
	// Requeue() = delay 0, RequeueAfter(5) = delay 5.
	// Minimum delay wins, so Requeue() wins.
	outcome := MergeReconciles(rf.Requeue(), rf.RequeueAfter(5))

	if !outcome.ShouldReturn() {
		t.Fatalf("expected ShouldReturn() == true")
	}

	res, err := outcome.ToCtrl()
	if err != nil {
		t.Fatalf("expected err to be nil, got %v", err)
	}
	if !res.Requeue { //nolint:staticcheck // testing deprecated Requeue field
		t.Fatalf("expected Requeue to be true (delay=0 wins)")
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected RequeueAfter to be 0 when Requeue is set, got %v", res.RequeueAfter)
	}
}
