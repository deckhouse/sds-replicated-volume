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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

// DefaultRetryBackoff is the default exponential backoff for transient Kubernetes API errors:
// 500ms base, factor 2.0 growth, +/-20% jitter, with the base delay capped at 30s before jitter
// is applied (so the effective per-attempt delay is in [24s, 36s] once the cap is reached). The
// retry loop is unbounded (infinite until context cancellation); these parameters only shape
// the per-attempt delay, not the number of attempts.
var DefaultRetryBackoff = wait.Backoff{
	Duration: 500 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.2,
	Cap:      30 * time.Second,
}

// IsTransientAPIError reports errors worth retrying against the Kubernetes API server:
// network blips, server overload (5xx), throttling (429), timeouts, and connection/DNS failures.
//
// Conflict (StatusReasonConflict, HTTP 409) is classified as transient so that read-modify-write
// sequences wrapped in a single RetryTransient call re-read the object and retry the whole
// operation on optimistic concurrency conflicts. Callers MUST wrap the entire Get->Modify->Patch
// sequence in one RetryTransient call (not each call individually) for conflict retries to work
// correctly.
//
// AlreadyExists (also HTTP 409 but StatusReasonAlreadyExists) is NOT transient: retrying a Create
// that already exists will not help. Idempotent call sites must swallow AlreadyExists themselves
// (see createIfNotExists); a bare Create inside RetryTransient without swallowing will stop the
// loop with a permanent error.
//
// context.Canceled is NOT transient (caller intended to stop). NotFound and other 4xx client
// errors are NOT transient (retrying will not help).
func IsTransientAPIError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		code := int(statusErr.Status().Code)
		switch code {
		case http.StatusRequestTimeout, http.StatusTooManyRequests,
			http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) {
			return true
		}
		if apierrors.IsConflict(err) {
			return true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// DNS / connection failures often surface as *net.OpError without Timeout().
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// RetryTransient runs fn until it succeeds, returns a non-transient error, or ctx is cancelled.
// Transient errors (per IsTransientAPIError) are retried with exponential backoff shaped by
// backoff, with jitter, capped at backoff.Cap. The retry is unbounded: migration is expected to
// complete eventually. Use a cancellable context (e.g. SIGTERM) to stop the loop.
//
// label is included in warning log lines to identify which step is retrying.
func RetryTransient(ctx context.Context, log *slog.Logger, backoff wait.Backoff, label string, fn func() error) error {
	attempt := 0
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if !IsTransientAPIError(err) {
			return err
		}
		attempt++
		delay := nextBackoffDelay(backoff, attempt)
		log.Warn("transient error, retrying", "step", label, "attempt", attempt, "delay", delay, "err", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// nextBackoffDelay computes the delay for the given 1-based attempt using exponential growth
// (backoff.Duration * Factor^(attempt-1)), capped at backoff.Cap, with jitter applied. Once the
// cap is reached, subsequent delays stay at cap+jitter. If Factor <= 1, the delay stays constant
// (useful for tests where deterministic timing is needed).
func nextBackoffDelay(b wait.Backoff, attempt int) time.Duration {
	d := b.Duration
	for i := 1; i < attempt; i++ {
		if b.Factor <= 1 {
			break
		}
		d = time.Duration(float64(d) * b.Factor)
		if b.Cap > 0 && d > b.Cap {
			d = b.Cap
			break
		}
	}
	if b.Jitter > 0 {
		d = wait.Jitter(d, b.Jitter)
	}
	return d
}
