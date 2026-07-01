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
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// validateLifecyclePositions checks that lifecycle matchers appear in valid
// positions within a sequence. Rules:
//   - Deleted() must be followed by PresentAgain() or nothing (end).
//   - PresentAgain() must be immediately preceded by Deleted().
//   - Present() must not follow Deleted() directly (use PresentAgain).
func validateLifecyclePositions(matchers []types.GomegaMatcher) {
	for i, m := range matchers {
		if match.IsDeletedMatcher(m) {
			if i < len(matchers)-1 && !match.IsPresentAgainMatcher(matchers[i+1]) {
				panic("Deleted() must be followed by PresentAgain() or be the last step")
			}
		}
		if match.IsPresentAgainMatcher(m) {
			if i > 0 && !match.IsDeletedMatcher(matchers[i-1]) {
				panic("PresentAgain() must be the first step or immediately preceded by Deleted()")
			}
		}
		if match.IsPresentMatcher(m) {
			if i > 0 && match.IsDeletedMatcher(matchers[i-1]) {
				panic("Present() must not follow Deleted() directly; use PresentAgain()")
			}
		}
	}
}

// sequence tracks the cursor through an ordered list of matchers.
// Steps can be skipped (gap tolerance for event coalescing), but every
// observed state must match the current step or some future step.
// Unknown states cause fail-fast.
type sequence struct {
	steps        []types.GomegaMatcher
	cursor       int
	done         bool
	err          error
	registeredAt string
}

// stepMatchesEvent reports whether a step matches the given event.
// Lifecycle matchers are checked by event type; regular matchers by object.
func stepMatchesEvent(step types.GomegaMatcher, eventType watch.EventType, obj client.Object) bool {
	if match.IsDeletedMatcher(step) {
		return eventType == watch.Deleted
	}
	if match.IsPresentMatcher(step) || match.IsPresentAgainMatcher(step) {
		return eventType != watch.Deleted
	}
	if eventType == watch.Deleted {
		return false
	}
	matched, _ := match.MatchObject(step, obj)
	return matched
}

// advance processes a new snapshot through the cursor logic.
//
//  1. Already done → skip.
//  2. Current step matches → stay (or done if last).
//     For Deleted() with a following PresentAgain(), advance to PresentAgain
//     instead of completing.
//  3. Any future step matches (gap tolerance) → advance.
//     Gap tolerance stops at Present/PresentAgain boundaries — these
//     mark lifecycle transitions that cannot be skipped over.
//  4. Delete event with no Deleted() ahead → FAIL.
//  5. Non-delete event matches nothing → FAIL (unexpected state).
func (s *sequence) advance(eventType watch.EventType, obj client.Object) error {
	if s.done {
		return nil
	}

	if stepMatchesEvent(s.steps[s.cursor], eventType, obj) {
		return s.advanceMatched(s.cursor)
	}

	for i := s.cursor + 1; i < len(s.steps); i++ {
		if match.IsPresentMatcher(s.steps[i]) || match.IsPresentAgainMatcher(s.steps[i]) {
			break
		}
		if stepMatchesEvent(s.steps[i], eventType, obj) {
			s.cursor = i
			return s.advanceMatched(i)
		}
	}

	s.done = true
	if eventType == watch.Deleted {
		s.err = fmt.Errorf("object deleted while sequence in progress at step %d\n  Registered at: %s", s.cursor, s.registeredAt)
	} else {
		_, detail := match.MatchObject(s.steps[s.cursor], obj)
		s.err = fmt.Errorf("at step %d: unexpected state: %s\n  Registered at: %s", s.cursor, detail, s.registeredAt)
	}
	return s.err
}

// advanceMatched handles a successful match at position idx.
//   - Deleted() followed by PresentAgain(): advance to PresentAgain (reincarnation).
//   - Present()/PresentAgain(): always advance past (transitional, not stationary).
//   - Regular matchers: stay on current step (stationary until next state).
//   - Last step: done.
func (s *sequence) advanceMatched(idx int) error {
	if match.IsDeletedMatcher(s.steps[idx]) && idx+1 < len(s.steps) && match.IsPresentAgainMatcher(s.steps[idx+1]) {
		s.cursor = idx + 1
		return nil
	}
	if match.IsPresentMatcher(s.steps[idx]) || match.IsPresentAgainMatcher(s.steps[idx]) {
		if idx < len(s.steps)-1 {
			s.cursor = idx + 1
		} else {
			s.done = true
		}
		return nil
	}
	if idx == len(s.steps)-1 {
		s.done = true
	}
	return nil
}

func callerLocation(skip int) string { //nolint:unparam // skip is semantically meaningful (call stack depth)
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// Follows registers a sequence starting from the current state.
// Steps must appear in order; intermediate steps may be skipped
// (gap tolerance). Unknown states cause fail-fast.
//
// The sequence is seeded with the latest snapshot so that it can
// match (or complete) immediately if the current state already
// satisfies it.
//
// If the object is currently deleted and the first step is not
// Deleted() or PresentAgain(), the sequence fails immediately.
func (t *TrackedObject[T]) Follows(matchers ...types.GomegaMatcher) {
	validateLifecyclePositions(matchers)
	s := &sequence{
		steps:        matchers,
		registeredAt: callerLocation(2),
	}
	t.mu.Lock()
	if t.deleted && len(matchers) > 0 &&
		!match.IsDeletedMatcher(matchers[0]) &&
		!match.IsPresentAgainMatcher(matchers[0]) {
		if t.failedErr == nil {
			t.failedErr = fmt.Errorf("sequence registered on %s while object is deleted (first step is not Deleted/PresentAgain)\n  Registered at: %s", t.objName, s.registeredAt)
			close(t.failedCh)
		}
	}
	if !t.deleted && len(t.snapshots) > 0 && !s.done {
		latest := t.snapshots[len(t.snapshots)-1]
		if err := s.advance(latest.EventType, latest.Object); err != nil {
			if t.failedErr == nil {
				t.failedErr = fmt.Errorf("sequence failed on %s (snapshot #%d, seed):\n  %s", t.objName, latest.ID, err)
				close(t.failedCh)
			}
		}
	}
	t.sequences = append(t.sequences, s)
	t.mu.Unlock()
}

// FollowsFromStart registers a sequence and replays all
// accumulated history through the cursor. Use for freshly created
// objects where events may have already arrived.
// Not supported on lite TrackedObjects (created with NewLiteTrackedObject).
func (t *TrackedObject[T]) FollowsFromStart(matchers ...types.GomegaMatcher) {
	validateLifecyclePositions(matchers)
	if t.keepOnlyLast {
		panic("FollowsFromStart is not supported on lite TrackedObjects")
	}
	s := &sequence{
		steps:        matchers,
		registeredAt: callerLocation(2),
	}

	t.mu.Lock()

	// Replay existing snapshots.
	for _, snap := range t.snapshots {
		if err := s.advance(snap.EventType, snap.Object); err != nil {
			if t.failedErr == nil {
				t.failedErr = fmt.Errorf("sequence failed on %s (snapshot #%d, replay):\n  %s", t.objName, snap.ID, err)
				close(t.failedCh)
			}
			break
		}
	}

	t.sequences = append(t.sequences, s)
	t.mu.Unlock()
}

// awaitOneSequence blocks until a single sequence is done
// (completed or failed) or the context is cancelled.
func (t *TrackedObject[T]) awaitOneSequence(ctx context.Context, s *sequence) {
	ginkgo.GinkgoHelper()
	for {
		t.mu.RLock()
		if s.done {
			if s.err != nil {
				t.mu.RUnlock()
				gomega.Default.(*gomega.WithT).Fail(
					fmt.Sprintf("sequence failed on %s:\n  %s", t.objName, s.err), 1)
				return
			}
			t.mu.RUnlock()
			return
		}
		ch := t.notify
		failed := t.failedCh
		t.mu.RUnlock()

		select {
		case <-ch:
		case <-failed:
			t.checkFailed()
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.mu.RLock()
				step := s.cursor
				total := len(s.steps)
				regAt := s.registeredAt
				isDone := s.done
				t.mu.RUnlock()
				if !isDone {
					gomega.Default.(*gomega.WithT).Fail(
						fmt.Sprintf("%s: sequence timed out at step %d/%d\n  Registered at: %s",
							t.objName, step+1, total, regAt), 1)
				}
			}
			return
		}
	}
}

// AwaitFollowed blocks until all registered sequences are done
// (completed or failed) or the context is cancelled.
// Calls Fail on the first error.
func (t *TrackedObject[T]) AwaitFollowed(ctx context.Context) {
	ginkgo.GinkgoHelper()
	t.checkFailed()
	for {
		t.mu.RLock()
		allDone := true
		for _, s := range t.sequences {
			if !s.done {
				allDone = false
				break
			}
		}
		if allDone {
			for _, s := range t.sequences {
				if s.err != nil {
					t.mu.RUnlock()
					gomega.Default.(*gomega.WithT).Fail(
						fmt.Sprintf("sequence failed on %s:\n  %s", t.objName, s.err), 1)
					return
				}
			}
			t.mu.RUnlock()
			return
		}
		ch := t.notify
		failed := t.failedCh
		t.mu.RUnlock()

		select {
		case <-ch:
		case <-failed:
			t.checkFailed()
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.mu.RLock()
				var pendingCount int
				var firstStep, firstTotal int
				var firstReg string
				for _, seq := range t.sequences {
					if !seq.done {
						pendingCount++
						if pendingCount == 1 {
							firstStep = seq.cursor
							firstTotal = len(seq.steps)
							firstReg = seq.registeredAt
						}
					}
				}
				t.mu.RUnlock()
				if pendingCount > 0 {
					gomega.Default.(*gomega.WithT).Fail(
						fmt.Sprintf("%s: AwaitFollowed timed out, %d sequence(s) pending; first at step %d/%d\n  Registered at: %s",
							t.objName, pendingCount, firstStep+1, firstTotal, firstReg), 1)
				}
			}
			return
		}
	}
}

// AwaitSequence registers a sequence starting from the current state
// and blocks until it is done.
//
// The sequence is seeded with the latest snapshot so that it completes
// immediately when the current state already satisfies the sequence.
// Combines Follows + awaitOneSequence in a single call.
func (t *TrackedObject[T]) AwaitSequence(ctx context.Context, matchers ...types.GomegaMatcher) {
	ginkgo.GinkgoHelper()
	t.checkFailed()
	validateLifecyclePositions(matchers)
	s := &sequence{
		steps:        matchers,
		registeredAt: callerLocation(2),
	}
	t.mu.Lock()
	if t.deleted && len(matchers) > 0 &&
		!match.IsDeletedMatcher(matchers[0]) &&
		!match.IsPresentAgainMatcher(matchers[0]) {
		if t.failedErr == nil {
			t.failedErr = fmt.Errorf("sequence registered on %s while object is deleted (first step is not Deleted/PresentAgain)\n  Registered at: %s", t.objName, s.registeredAt)
			close(t.failedCh)
		}
	}
	if !t.deleted && len(t.snapshots) > 0 && !s.done {
		latest := t.snapshots[len(t.snapshots)-1]
		if err := s.advance(latest.EventType, latest.Object); err != nil {
			if t.failedErr == nil {
				t.failedErr = fmt.Errorf("sequence failed on %s (snapshot #%d, seed):\n  %s", t.objName, latest.ID, err)
				close(t.failedCh)
			}
		}
	}
	t.sequences = append(t.sequences, s)
	t.mu.Unlock()
	t.awaitOneSequence(ctx, s)
	t.checkFailed()
}

// AwaitSequenceFromStart registers a sequence, replays all accumulated
// history through the cursor, and blocks until it is done.
// Combines FollowsFromStart + awaitOneSequence in a single call.
// Not supported on lite TrackedObjects (created with NewLiteTrackedObject).
func (t *TrackedObject[T]) AwaitSequenceFromStart(ctx context.Context, matchers ...types.GomegaMatcher) {
	ginkgo.GinkgoHelper()
	t.checkFailed()
	validateLifecyclePositions(matchers)
	if t.keepOnlyLast {
		panic("AwaitSequenceFromStart is not supported on lite TrackedObjects")
	}
	s := &sequence{
		steps:        matchers,
		registeredAt: callerLocation(2),
	}

	t.mu.Lock()

	for _, snap := range t.snapshots {
		if err := s.advance(snap.EventType, snap.Object); err != nil {
			if t.failedErr == nil {
				t.failedErr = fmt.Errorf("sequence failed on %s (snapshot #%d, replay):\n  %s", t.objName, snap.ID, err)
				close(t.failedCh)
			}
			break
		}
	}

	t.sequences = append(t.sequences, s)
	t.mu.Unlock()
	t.awaitOneSequence(ctx, s)
	t.checkFailed()
}
