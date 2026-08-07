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

package sync

import (
	"context"
	"errors"
	gosync "sync"
	"testing"
	"time"
)

func TestOnceUpgraderDisabled(t *testing.T) {
	calls := 0
	u := NewOnceUpgrader(false, func(context.Context) error {
		calls++
		return nil
	})

	for range 3 {
		if err := u.EnsureUpgraded(context.Background()); err != nil {
			t.Fatalf("EnsureUpgraded() error = %v; want nil", err)
		}
	}

	if calls != 0 {
		t.Errorf("upgrade function ran %d times on a disabled upgrader; want 0", calls)
	}
}

func TestOnceUpgraderSucceedsOnce(t *testing.T) {
	calls := 0
	u := NewOnceUpgrader(true, func(context.Context) error {
		calls++
		return nil
	})

	for range 3 {
		if err := u.EnsureUpgraded(context.Background()); err != nil {
			t.Fatalf("EnsureUpgraded() error = %v; want nil", err)
		}
	}

	if calls != 1 {
		t.Errorf("upgrade function ran %d times after a successful attempt; want exactly 1", calls)
	}
}

func TestOnceUpgraderRetriesUntilSuccess(t *testing.T) {
	failure := errors.New("upgrade failed")
	calls := 0
	u := NewOnceUpgrader(true, func(context.Context) error {
		calls++
		if calls < 3 {
			return failure
		}
		return nil
	})

	for attempt := 1; attempt <= 2; attempt++ {
		if err := u.EnsureUpgraded(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("attempt %d: EnsureUpgraded() error = %v; want %v", attempt, err, failure)
		}
	}

	if err := u.EnsureUpgraded(context.Background()); err != nil {
		t.Fatalf("attempt 3: EnsureUpgraded() error = %v; want nil", err)
	}
	if err := u.EnsureUpgraded(context.Background()); err != nil {
		t.Fatalf("EnsureUpgraded() after success: error = %v; want nil", err)
	}
	if calls != 3 {
		t.Errorf("upgrade function ran %d times; want it to stop at the successful third attempt", calls)
	}
}

func TestOnceUpgraderOneAttemptPerCaller(t *testing.T) {
	failure := errors.New("upgrade failed")
	calls := 0
	u := NewOnceUpgrader(true, func(context.Context) error {
		calls++
		return failure
	})

	err := u.EnsureUpgraded(context.Background())

	if !errors.Is(err, failure) {
		t.Errorf("EnsureUpgraded() error = %v; want %v", err, failure)
	}
	if calls != 1 {
		t.Errorf("upgrade function ran %d times in one EnsureUpgraded call; want exactly 1", calls)
	}
}

func TestOnceUpgraderBlocksConcurrentCallers(t *testing.T) {
	const waiters = 20

	tests := []struct {
		name       string
		attemptErr error
		// attempts the wave as a whole performs
		wantCalls int
	}{
		{
			name:      "successful attempt releases every blocked caller",
			wantCalls: 1,
		},
		{
			name:       "failing attempts pass the ticket to the next caller",
			attemptErr: errors.New("upgrade failed"),
			wantCalls:  waiters,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu    gosync.Mutex
				calls int
			)
			entered := make(chan struct{})
			release := make(chan struct{})
			u := NewOnceUpgrader(true, func(context.Context) error {
				mu.Lock()
				calls++
				first := calls == 1
				mu.Unlock()
				if first {
					// park here so the others pile up behind the barrier
					close(entered)
					<-release
				}
				return tt.attemptErr
			})

			errs := make([]error, waiters)
			var wg gosync.WaitGroup

			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[0] = u.EnsureUpgraded(context.Background())
			}()
			<-entered

			for i := 1; i < waiters; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[i] = u.EnsureUpgraded(context.Background())
				}()
			}

			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			inFlightCalls := calls
			mu.Unlock()
			if inFlightCalls != 1 {
				t.Errorf("%d attempts started while one was in flight; want the barrier to hold at 1", inFlightCalls)
			}

			close(release)
			wg.Wait()

			if calls != tt.wantCalls {
				t.Errorf("upgrade function ran %d times for a wave of %d callers; want %d", calls, waiters, tt.wantCalls)
			}
			for i, err := range errs {
				switch {
				case tt.attemptErr != nil && !errors.Is(err, tt.attemptErr):
					t.Errorf("caller %d: error = %v; want %v", i, err, tt.attemptErr)
				case tt.attemptErr == nil && err != nil:
					t.Errorf("caller %d: error = %v; want nil", i, err)
				}
			}
		})
	}
}

func TestOnceUpgraderContextEndsWhileWaiting(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	u := NewOnceUpgrader(true, func(context.Context) error {
		calls++
		close(entered)
		<-release
		return nil
	})

	go func() { _ = u.EnsureUpgraded(context.Background()) }()
	<-entered
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := u.EnsureUpgraded(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("EnsureUpgraded() error = %v; want it to wrap context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("upgrade function ran %d times; want only the attempt that holds the ticket", calls)
	}
}

func TestOnceUpgraderPassesContextToUpgradeFn(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "value")

	var got any
	u := NewOnceUpgrader(true, func(fnCtx context.Context) error {
		got = fnCtx.Value(ctxKey{})
		return nil
	})

	if err := u.EnsureUpgraded(ctx); err != nil {
		t.Fatalf("EnsureUpgraded() error = %v", err)
	}

	if got != "value" {
		t.Errorf("upgrade function saw context value %v; want %q", got, "value")
	}
}
