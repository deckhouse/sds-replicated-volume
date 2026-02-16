package sync

import (
	"context"
	"time"
)

// Wait reads values from ch until predicate returns true, then returns the
// matching value and true. If the channel closes before a match, returns the
// zero value of T and false.
func Wait[T any](ch <-chan T, predicate func(T) bool) (T, bool) {
	for v := range ch {
		if predicate(v) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// WaitWithTimeout is like Wait but creates a child context with the given
// timeout. If the timeout expires (and the channel closes as a result), it
// returns the zero value of T and false.
//
// The caller is responsible for ensuring that the channel closes when the
// context is cancelled (e.g. a Kubernetes watcher bound to the context).
func WaitWithTimeout[T any](parentCtx context.Context, timeout time.Duration, ch <-chan T, predicate func(T) bool) (T, bool) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			var zero T
			return zero, false
		case v, ok := <-ch:
			if !ok {
				var zero T
				return zero, false
			}
			if predicate(v) {
				return v, true
			}
		}
	}
}
