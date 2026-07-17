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

package kubeutils

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestIsTransientAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"503 ServiceUnavailable", apierrors.NewServiceUnavailable("unavailable"), true},
		{"429 TooManyRequests", apierrors.NewTooManyRequests("throttled", 0), true},
		{"404 NotFound", apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "missing"), false},
		{"409 Conflict", apierrors.NewConflict(schema.GroupResource{Group: "", Resource: "pods"}, "conflict", errors.New("conflict")), true},
		{"409 AlreadyExists (not transient)", apierrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "configmaps"}, "x"), false},
		{"500 InternalServerError", apierrors.NewInternalError(errors.New("internal")), true},
		{"502 BadGateway", apierrors.NewGenericServerResponse(502, "put", schema.GroupResource{}, "x", "x", 0, true), true},
		{"504 GatewayTimeout", apierrors.NewGenericServerResponse(504, "put", schema.GroupResource{}, "x", "x", 0, true), true},
		{"408 RequestTimeout", &apierrors.StatusError{ErrStatus: metav1.Status{Code: http.StatusRequestTimeout}}, true},
		{"net.OpError", &net.OpError{Op: "dial", Net: "tcp", Addr: nil, Err: errors.New("connection refused")}, true},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTransientAPIError(tt.err); got != tt.want {
				t.Fatalf("IsTransientAPIError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryTransient_retriesThenSucceeds(t *testing.T) {
	t.Parallel()

	log := slog.Default()
	backoff := wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Jitter: 0}
	ctx := context.Background()
	var calls int
	err := RetryTransient(ctx, log, backoff, "test step", func() error {
		calls++
		if calls < 3 {
			return apierrors.NewServiceUnavailable("unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryTransient() = %v, want nil", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryTransient_permanentError(t *testing.T) {
	t.Parallel()

	log := slog.Default()
	backoff := wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Jitter: 0}
	want := errors.New("permanent")
	err := RetryTransient(context.Background(), log, backoff, "test step", func() error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("RetryTransient() = %v, want %v", err, want)
	}
}

func TestRetryTransient_contextCanceled(t *testing.T) {
	t.Parallel()

	log := slog.Default()
	backoff := wait.Backoff{Duration: time.Hour, Factor: 1.0, Jitter: 0}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RetryTransient(ctx, log, backoff, "test step", func() error {
		return apierrors.NewServiceUnavailable("unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RetryTransient() = %v, want context.Canceled", err)
	}
}

func TestNextBackoffDelay(t *testing.T) {
	t.Parallel()

	t.Run("exponential growth", func(t *testing.T) {
		t.Parallel()
		b := wait.Backoff{Duration: 100 * time.Millisecond, Factor: 2.0, Jitter: 0, Cap: 0}
		cases := []struct {
			attempt int
			want    time.Duration
		}{
			{1, 100 * time.Millisecond},
			{2, 200 * time.Millisecond},
			{3, 400 * time.Millisecond},
			{5, 1600 * time.Millisecond},
		}
		for _, tc := range cases {
			got := nextBackoffDelay(b, tc.attempt)
			if got != tc.want {
				t.Errorf("attempt %d: got %v, want %v", tc.attempt, got, tc.want)
			}
		}
	})

	t.Run("cap", func(t *testing.T) {
		t.Parallel()
		b := wait.Backoff{Duration: 100 * time.Millisecond, Factor: 2.0, Jitter: 0, Cap: 500 * time.Millisecond}
		// attempt 4: 100*2^3=800ms, capped to 500ms
		d4 := nextBackoffDelay(b, 4)
		if d4 != 500*time.Millisecond {
			t.Errorf("attempt 4: got %v, want 500ms", d4)
		}
		// attempt 5: stays at cap
		d5 := nextBackoffDelay(b, 5)
		if d5 != 500*time.Millisecond {
			t.Errorf("attempt 5: got %v, want 500ms", d5)
		}
	})

	t.Run("factor<=1 constant", func(t *testing.T) {
		t.Parallel()
		b := wait.Backoff{Duration: 100 * time.Millisecond, Factor: 1.0, Jitter: 0}
		d1 := nextBackoffDelay(b, 1)
		if d1 != 100*time.Millisecond {
			t.Errorf("attempt 1: got %v, want 100ms", d1)
		}
		d3 := nextBackoffDelay(b, 3)
		if d3 != 100*time.Millisecond {
			t.Errorf("attempt 3: got %v, want 100ms", d3)
		}
	})

	t.Run("jitter range", func(t *testing.T) {
		t.Parallel()
		b := wait.Backoff{Duration: 100 * time.Millisecond, Factor: 1.0, Jitter: 0.5}
		minDelay := 100 * time.Millisecond
		maxDelay := 150 * time.Millisecond
		// Run multiple times to catch potential flakiness, but the range is tight.
		for range 50 {
			d := nextBackoffDelay(b, 1)
			if d < minDelay || d > maxDelay {
				t.Errorf("delay %v is outside expected range [%v, %v]", d, minDelay, maxDelay)
			}
		}
	})

	t.Run("cap_then_jitter_can_exceed_cap", func(t *testing.T) {
		t.Parallel()
		// Document that jitter is applied AFTER capping, so the effective delay can
		// exceed Cap by up to Jitter fraction. This is intentional and matches the
		// docstring on DefaultRetryBackoff.
		b := wait.Backoff{
			Duration: 500 * time.Millisecond,
			Factor:   2.0,
			Jitter:   0.2,
			Cap:      30 * time.Second,
		}
		// At attempt 20 the base delay is already capped at 30s before jitter.
		base := nextBackoffDelay(b, 20)
		if base > 36*time.Second {
			t.Errorf("delay after cap+jitter %v exceeds Cap*(1+Jitter)=%v", base, 36*time.Second)
		}
		if base < 24*time.Second {
			t.Errorf("delay after cap+jitter %v is below Cap*(1-Jitter)=%v", base, 24*time.Second)
		}
	})
}
