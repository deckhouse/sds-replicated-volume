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

package testkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// Snapshot is a single point-in-time capture of a Kubernetes object
// stored in a TrackedObject's history.
type Snapshot[T client.Object] struct {
	ID        int
	Timestamp time.Time
	EventType watch.EventType
	Object    T
}

// TrackedObject is the generic core for state-tracking test objects.
// It captures every observed state as an ordered history and provides
// matcher-based assertions, blocking waits, sequences, and continuous
// checks.
//
// TrackedObject is not created directly — domain types embed it and add
// typed convenience methods.
type TrackedObject[T client.Object] struct {
	mu     sync.RWMutex
	Cache  cache.Cache   // nil in unit tests
	Client client.Client // nil in unit tests
	GVK    schema.GroupVersionKind

	snapshots []Snapshot[T]
	notify    chan struct{}
	objName   string

	InformerRegs []InformerReg
	sequences    []*sequence
	checks       []*continuousCheck
	deleted      bool
	failedCh     chan struct{}
	failedErr    error

	keepOnlyLast bool
	totalEvents  int

	lifecycle Lifecycle[T]
}

// NewTrackedObject creates a TrackedObject with full snapshot history.
// cache and client may be nil in unit tests.
// lc configures the Create/Get lifecycle; zero value is safe.
func NewTrackedObject[T client.Object](c cache.Cache, cl client.Client, gvk schema.GroupVersionKind, name string, lc Lifecycle[T]) *TrackedObject[T] {
	return &TrackedObject[T]{
		Cache:     c,
		Client:    cl,
		GVK:       gvk,
		objName:   name,
		notify:    make(chan struct{}),
		failedCh:  make(chan struct{}),
		lifecycle: lc,
	}
}

// NewLiteTrackedObject creates a TrackedObject that keeps only the latest
// snapshot. Uses less memory for long-lived or shared objects.
// FollowsFromStart is not supported on lite objects.
// lc configures the Create/Get lifecycle; zero value is safe.
func NewLiteTrackedObject[T client.Object](c cache.Cache, cl client.Client, gvk schema.GroupVersionKind, name string, lc Lifecycle[T]) *TrackedObject[T] {
	return &TrackedObject[T]{
		Cache:        c,
		Client:       cl,
		GVK:          gvk,
		objName:      name,
		notify:       make(chan struct{}),
		failedCh:     make(chan struct{}),
		keepOnlyLast: true,
		lifecycle:    lc,
	}
}

// resetLocked resets the TrackedObject for a new lifecycle (reincarnation).
// Called when Added arrives after Deleted. Preserves checks (invariants)
// but clears lifecycle-specific state.
func (t *TrackedObject[T]) resetLocked() {
	t.snapshots = t.snapshots[:0]
	t.deleted = false
	t.sequences = nil
	t.failedErr = nil
	t.failedCh = make(chan struct{})
	t.totalEvents = 0
}

// InjectEvent appends a deep-copied snapshot to the history, runs
// continuous checks and advances sequence cursors, then wakes all
// goroutines blocked in Await or Object().
func (t *TrackedObject[T]) InjectEvent(eventType watch.EventType, obj T) {
	copied := obj.DeepCopyObject().(T)

	t.mu.Lock()

	if eventType == watch.Added && t.deleted {
		t.resetLocked()
	}

	snap := Snapshot[T]{
		Timestamp: time.Now(),
		EventType: eventType,
		Object:    copied,
	}

	if t.keepOnlyLast {
		snap.ID = t.totalEvents
		t.totalEvents++
		if len(t.snapshots) == 0 {
			t.snapshots = append(t.snapshots, snap)
		} else {
			t.snapshots[0] = snap
		}
	} else {
		snap.ID = len(t.snapshots)
		t.snapshots = append(t.snapshots, snap)
	}

	// Deletion auto-suspend: skip all checks when the object is deleted.
	if eventType == watch.Deleted {
		t.deleted = true
	}

	// Run continuous checks (skip if deleted).
	var checkErr error
	for _, c := range t.checks {
		if t.deleted {
			break
		}
		if err := c.evaluate(copied); err != nil && checkErr == nil {
			checkErr = err
		}
	}

	// Advance sequence cursors.
	var seqErr error
	for _, s := range t.sequences {
		if err := s.advance(eventType, copied); err != nil && seqErr == nil {
			seqErr = err
		}
	}

	// If any check or sequence failed, record and signal.
	if t.failedErr == nil {
		if checkErr != nil {
			t.failedErr = fmt.Errorf("check violated on %s (snapshot #%d):\n  %s", t.objName, snap.ID, checkErr)
			close(t.failedCh)
		} else if seqErr != nil {
			t.failedErr = fmt.Errorf("sequence failed on %s (snapshot #%d):\n  %s", t.objName, snap.ID, seqErr)
			close(t.failedCh)
		}
	}

	ch := t.notify
	t.notify = make(chan struct{})
	t.mu.Unlock()

	close(ch)
}

// Object returns a deep copy of the tracked Kubernetes object from
// the latest snapshot. Fails if no snapshots exist yet or if the
// object is deleted — call Await(ctx, Present()) first.
// The returned object is safe to mutate without affecting the history.
func (t *TrackedObject[T]) Object() T {
	ginkgo.GinkgoHelper()
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.snapshots) == 0 {
		gomega.Default.(*gomega.WithT).Fail(fmt.Sprintf("%s: Object() called but no snapshots exist yet; call Await(ctx, Present()) first", t.objName), 1)
		var zero T
		return zero
	}
	if t.deleted {
		gomega.Default.(*gomega.WithT).Fail(fmt.Sprintf("%s: Object() called but object is deleted", t.objName), 1)
		var zero T
		return zero
	}
	return t.snapshots[len(t.snapshots)-1].Object.DeepCopyObject().(T)
}

// snapshotCount returns the number of snapshots in the history.
func (t *TrackedObject[T]) snapshotCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.snapshots)
}

// Name returns the object name used in error messages.
func (t *TrackedObject[T]) Name() string {
	return t.objName
}

// Await blocks until a snapshot satisfies the matcher or the context
// is cancelled. Also wakes on failedCh if a continuous check or
// sequence fails. Calls Fail immediately if the object is deleted,
// because a deleted object can never satisfy the awaited condition.
func (t *TrackedObject[T]) Await(ctx context.Context, m types.GomegaMatcher) {
	ginkgo.GinkgoHelper()
	t.checkFailed()
	t.awaitLoop(ctx, m)
	t.checkFailed()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.failOnTimeout(m)
	}
}

// awaitLoop is the blocking core of Await. It records failures in
// failedErr (via recordFailure) but never calls Fail — the caller
// is responsible for surfacing failures via checkFailed.
//
// Lifecycle matchers receive special treatment:
//   - Deleted()      — matches when t.deleted; never fail-fast
//   - Present()      — matches when !t.deleted; fail-fast on deletion
//   - PresentAgain() — matches when !t.deleted; tolerates deletion (reincarnation)
//   - Regular        — matches via MatchObject; fail-fast on deletion
func (t *TrackedObject[T]) awaitLoop(ctx context.Context, m types.GomegaMatcher) {
	skipDeletedFailFast := match.IsDeletedMatcher(m) || match.IsPresentAgainMatcher(m)

	for {
		t.mu.RLock()
		if len(t.snapshots) > 0 {
			var matched bool
			switch {
			case match.IsDeletedMatcher(m):
				matched = t.deleted
			case match.IsPresentMatcher(m) || match.IsPresentAgainMatcher(m):
				matched = !t.deleted
			default:
				obj := t.snapshots[len(t.snapshots)-1].Object
				matched, _ = match.MatchObject(m, obj)
			}

			if matched {
				t.mu.RUnlock()
				return
			}
			if t.deleted && !skipDeletedFailFast {
				t.mu.RUnlock()
				t.recordFailure(fmt.Sprintf("%s: object deleted while awaiting condition", t.objName))
				return
			}
		}
		ch := t.notify
		failed := t.failedCh
		t.mu.RUnlock()

		select {
		case <-ch:
		case <-failed:
			return
		case <-ctx.Done():
			return
		}
	}
}

// failOnTimeout is called when Await returns with a cancelled context.
// It re-checks the condition against the latest snapshot (handling the
// race where the condition was met concurrently with context cancellation)
// and calls Fail with a descriptive message if the condition is not met.
func (t *TrackedObject[T]) failOnTimeout(m types.GomegaMatcher) {
	ginkgo.GinkgoHelper()
	t.mu.RLock()
	snapCount := len(t.snapshots)
	deleted := t.deleted
	var lastObj T
	if snapCount > 0 {
		lastObj = t.snapshots[len(t.snapshots)-1].Object
	}
	t.mu.RUnlock()

	switch {
	case match.IsDeletedMatcher(m):
		if !deleted {
			gomega.Default.(*gomega.WithT).Fail(
				fmt.Sprintf("%s %s: timed out waiting for deletion", t.GVK.Kind, t.objName), 1)
		}
	case match.IsPresentMatcher(m):
		if snapCount == 0 || deleted {
			msg := "timed out waiting for object to appear"
			if deleted {
				msg = "timed out: object was deleted"
			}
			gomega.Default.(*gomega.WithT).Fail(
				fmt.Sprintf("%s %s: %s", t.GVK.Kind, t.objName, msg), 1)
		}
	case match.IsPresentAgainMatcher(m):
		if deleted || snapCount == 0 {
			gomega.Default.(*gomega.WithT).Fail(
				fmt.Sprintf("%s %s: timed out waiting for object to reappear", t.GVK.Kind, t.objName), 1)
		}
	default:
		if snapCount == 0 {
			gomega.Default.(*gomega.WithT).Fail(
				fmt.Sprintf("%s %s: timed out awaiting condition (no events received)", t.GVK.Kind, t.objName), 1)
			return
		}
		matched, detail := match.MatchObject(m, lastObj)
		if !matched {
			gomega.Default.(*gomega.WithT).Fail(
				fmt.Sprintf("%s %s: timed out awaiting condition\n  %s", t.GVK.Kind, t.objName, detail), 1)
		}
	}
}

// IsDeleted reports whether the last observed event was a Delete.
func (t *TrackedObject[T]) IsDeleted() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.snapshots) == 0 {
		return false
	}
	return t.snapshots[len(t.snapshots)-1].EventType == watch.Deleted
}

// IsPresent reports whether the object has been observed and is not deleted.
func (t *TrackedObject[T]) IsPresent() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.snapshots) > 0 && !t.deleted
}

// recordFailure records a failure message in failedErr and closes
// failedCh (if not already failed). Safe to call without holding mu.
func (t *TrackedObject[T]) recordFailure(msg string) {
	t.mu.Lock()
	if t.failedErr == nil {
		t.failedErr = fmt.Errorf("%s", msg)
		close(t.failedCh)
	}
	t.mu.Unlock()
}

// checkFailed calls Fail if a violation has been recorded.
// Called from test-goroutine methods (Await, AwaitSequence, etc.).
// Uses Gomega's fail handler (not ginkgo.Fail directly) so that
// InterceptGomegaFailure can intercept it in unit tests.
func (t *TrackedObject[T]) checkFailed() {
	ginkgo.GinkgoHelper()
	t.mu.RLock()
	err := t.failedErr
	t.mu.RUnlock()
	if err != nil {
		gomega.Default.(*gomega.WithT).Fail(err.Error(), 1)
	}
}

// watchSelf registers an informer handler that routes events for this
// object (matched by name) into the snapshot history.
func (t *TrackedObject[T]) watchSelf(ctx context.Context) {
	if t.Cache == nil {
		return
	}
	reg := RegisterInformer[T](ctx, t.Cache, t.GVK, "watch "+t.objName,
		func(obj T) bool { return obj.GetName() == t.objName },
		func(et watch.EventType, obj T) { t.InjectEvent(et, obj) },
	)
	t.InformerRegs = append(t.InformerRegs, reg)
}

// unwatchSelf deregisters all informer handlers owned by this TrackedObject.
func (t *TrackedObject[T]) unwatchSelf() {
	RemoveInformerRegs(t.InformerRegs)
	t.InformerRegs = nil
}
