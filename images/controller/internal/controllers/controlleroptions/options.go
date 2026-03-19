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

package controlleroptions

import (
	"log/slog"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// DefaultRateLimiter returns the shared rate limiter used by all controllers in this binary.
//
// It combines two limiters (max of both delays is used):
//   - Per-item exponential backoff: 5ms base, 1 minute max.
//   - Global token bucket: 100 QPS, burst 500.
func DefaultRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
			5*time.Millisecond, // baseDelay
			1*time.Minute,      // maxDelay
		),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{
			Limiter: rate.NewLimiter(rate.Limit(100), 500),
		},
	)
}

// NewQueueWithHeartbeat creates a priority queue with a background heartbeat that
// periodically calls Len(). Len() internally flushes the addBuffer, working around
// a deadlock in controller-runtime's priority queue where notifications on buffered(1)
// channels are silently dropped under high contention (e.g. bursts of 409 conflicts),
// causing items to rot in addBuffer and all workers to stall permanently.
//
// This is analogous to the 10-second heartbeat in the old client-go workqueue that
// prevents similar stalls in its delaying queue.
func NewQueueWithHeartbeat(controllerName string, rateLimiter workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request] {
	pq := priorityqueue.New(controllerName, func(o *priorityqueue.Opts[reconcile.Request]) {
		o.RateLimiter = rateLimiter
	})

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if pq.ShuttingDown() {
				return
			}
			depth := pq.Len()
			if depth > 0 {
				slog.Warn("queue heartbeat: flushed stale items from addBuffer",
					"controller", controllerName,
					"ready_depth", depth,
				)
			}
		}
	}()

	return pq
}
