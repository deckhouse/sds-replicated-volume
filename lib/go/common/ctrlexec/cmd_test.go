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

package ctrlexec

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// withWaitDelay is a small test helper that builds a configure func for
// ExecCommandContext setting only WaitDelay.
func withWaitDelay(d time.Duration) func(*exec.Cmd) {
	return func(c *exec.Cmd) { c.WaitDelay = d }
}

// TestWithTimeout_DoesNotKillImmediately verifies that WithTimeout does NOT
// cancel the context as soon as the factory returns. A bare `echo` should
// complete successfully under a 10s timeout — the previous `defer cancel()`
// bug would have killed the process on Start.
func TestWithTimeout_DoesNotKillImmediately(t *testing.T) {
	factory := WithTimeout(10*time.Second, ExecCommandContext(nil))

	cmd := factory(context.Background(), "echo", uniqueArg(t), "hi")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CombinedOutput: %v; out=%q", err, out)
	}
	if !strings.HasSuffix(string(out), "hi\n") {
		t.Fatalf("output = %q, want suffix %q", out, "hi\n")
	}
}

// TestWithTimeout_KillsAfterTimeout verifies that the wrapped context's
// timeout does kill a long-running process.
//
// `sleep` is invoked directly (not through a shell) so killing the process
// kills `sleep` itself with no orphaned-pipe hazard.
func TestWithTimeout_KillsAfterTimeout(t *testing.T) {
	factory := WithTimeout(100*time.Millisecond, ExecCommandContext(nil))

	cmd := factory(context.Background(), "sleep", "30")
	start := time.Now()
	_, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected non-nil error from timed-out command, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected command to be killed within ~100ms+slack, took %s", elapsed)
	}
}

// TestWithTimeout_ComposedWithCache verifies the recommended composition:
// WithCache(WithTimeout(...)) bounds the underlying process even when the
// caller's context is never cancelled. `sleep` is invoked directly to avoid
// the shell-fork pipe-orphan scenario tested separately.
func TestWithTimeout_ComposedWithCache(t *testing.T) {
	factory := WithCache(WithTimeout(200*time.Millisecond, ExecCommandContext(nil)))

	cmd := factory(context.Background(), "sleep", "30")
	start := time.Now()
	_, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected non-nil error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected ~200ms+slack, took %s", elapsed)
	}
}

// TestExecCommandContext_ConfigureSetsWaitDelay reproduces the
// orphaned-pipe hang: the shell exits immediately but the backgrounded
// `sleep 10` keeps the pipes open. With a configure func that sets
// WaitDelay, CombinedOutput must return shortly after the shell exits
// instead of waiting for the orphan to die.
func TestExecCommandContext_ConfigureSetsWaitDelay(t *testing.T) {
	factory := ExecCommandContext(withWaitDelay(200 * time.Millisecond))

	cmd := factory(context.Background(), "sh", "-c", "sleep 10 & exit 0")
	start := time.Now()
	_, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected exec.ErrWaitDelay, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("expected ~200ms+slack, took %s", elapsed)
	}
}

// TestExecCommandContext_ConfigureWaitDelayComposedWithCacheAndTimeout
// verifies the full recommended composition exercises both fixes — pipe
// orphan AND D-state safety paths line up. The shell exits immediately
// (orphan path), so we hit the WaitDelay branch in awaitGoroutines, not
// the WithTimeout kill path; total time should be ~WaitDelay.
func TestExecCommandContext_ConfigureWaitDelayComposedWithCacheAndTimeout(t *testing.T) {
	factory := WithCache(
		WithTimeout(30*time.Second,
			ExecCommandContext(withWaitDelay(200*time.Millisecond))))

	cmd := factory(context.Background(), "sh", "-c", "sleep 10 & exit 0")
	start := time.Now()
	_, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected exec.ErrWaitDelay, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("expected ~200ms+slack, took %s", elapsed)
	}
}
