package ctrlexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWithCache_BasicEcho verifies a trivial command runs and returns its output.
func TestWithCache_BasicEcho(t *testing.T) {
	factory := WithCache(ExecCommandContext(nil))

	cmd := factory(context.Background(), "echo", uniqueArg(t), "hello")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CombinedOutput: %v", err)
	}
	if got := string(out); !strings.HasSuffix(got, "hello\n") {
		t.Fatalf("output = %q, want suffix %q", got, "hello\n")
	}
}

// TestWithCache_DeduplicatesConcurrentCalls runs N concurrent invocations of
// the same command and verifies that only one underlying process actually
// ran, by counting PIDs appended to a marker file.
func TestWithCache_DeduplicatesConcurrentCalls(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	factory := WithCache(ExecCommandContext(nil))

	// $$ is the PID of this sh; sleep 1 keeps the process alive long enough
	// that all goroutines attach to the cached entry before it completes.
	script := fmt.Sprintf(`echo $$ >> %q; sleep 1`, marker)

	const N = 5
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cmd := factory(context.Background(), "sh", "-c", script)
			if _, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("CombinedOutput: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 underlying execution, marker contents (%d lines):\n%s", len(lines), data)
	}
}

// TestWithCache_ContextTimeoutDoesNotKillProcess verifies that when the
// caller's context expires while the command is still running, CombinedOutput
// returns context.DeadlineExceeded but the underlying process keeps running
// to completion.
func TestWithCache_ContextTimeoutDoesNotKillProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	factory := WithCache(ExecCommandContext(nil))

	script := fmt.Sprintf(`sleep 0.5; echo done > %q`, marker)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cmd := factory(ctx, "sh", "-c", script)
	_, err := cmd.CombinedOutput()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}

	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("marker should not exist yet (process should still be running): err=%v", statErr)
	}

	// Poll for the marker; the background goroutine should complete despite
	// the caller's context having been cancelled.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(marker); statErr == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("background goroutine did not complete and write marker")
}

// TestWithCache_AttachToStillRunning verifies that when a first call's context
// times out while the underlying command is still running, a second call with
// a fresh context attaches to the same goroutine instead of spawning a fresh
// process. We observe this by counting PIDs appended to a marker file.
func TestWithCache_AttachToStillRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	factory := WithCache(ExecCommandContext(nil))

	script := fmt.Sprintf(`echo $$ >> %q; sleep 1; printf done`, marker)
	mkCmd := func(ctx context.Context) Cmd {
		return factory(ctx, "sh", "-c", script)
	}

	// First call: ctx times out long before the 1s sleep completes.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel1()
	if _, err := mkCmd(ctx1).CombinedOutput(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first call: expected DeadlineExceeded, got %v", err)
	}

	// Second call: must attach to the still-running goroutine.
	out, err := mkCmd(context.Background()).CombinedOutput()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if string(out) != "done" {
		t.Fatalf("second call output = %q, want %q", out, "done")
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 underlying execution, marker contents (%d lines):\n%s", len(lines), data)
	}
}

// TestWithCache_StartWait verifies that Start/Wait round-trips successfully.
func TestWithCache_StartWait(t *testing.T) {
	factory := WithCache(ExecCommandContext(nil))

	cmd := factory(context.Background(), "sh", "-c", "exit 0", "--", uniqueArg(t))
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// uniqueArg returns a marker string that makes a command's cache key unique
// to a given test, avoiding cross-test collisions on the global cache.
func uniqueArg(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("ctrlexec-test-%s-%d", t.Name(), time.Now().UnixNano())
}
