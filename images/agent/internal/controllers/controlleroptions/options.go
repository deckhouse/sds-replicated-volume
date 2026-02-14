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
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
)

// DefaultRateLimiter returns the shared rate limiter used by all controllers in this binary.
//
// It combines two limiters (max of both delays is used):
//   - Per-item exponential backoff: 5ms base, 1 minute max.
//   - Global token bucket: 100 QPS, burst 500.
func DefaultRateLimiter[T comparable]() workqueue.TypedRateLimiter[T] {
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[T](
			5*time.Millisecond, // baseDelay
			1*time.Minute,      // maxDelay
		),
		&workqueue.TypedBucketRateLimiter[T]{
			Limiter: rate.NewLimiter(rate.Limit(100), 500),
		},
	)
}
