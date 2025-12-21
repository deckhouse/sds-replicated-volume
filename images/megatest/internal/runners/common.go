/*
Copyright 2025 Flant JSC

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

package runners

import (
	"context"
	"math/rand"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
)

const (
	// CleanupTimeout is the timeout for cleanup operations
	CleanupTimeout = 2 * time.Minute
)

// Runner represents a goroutine that can be started and stopped
type Runner interface {
	// Run starts the runner and blocks until the context is cancelled
	Run(ctx context.Context) error
}

// randomDuration returns a random duration between min and max
func randomDuration(d config.DurationMinMax) time.Duration {
	if d.Max <= d.Min {
		return d.Min
	}
	delta := d.Max - d.Min
	//nolint:gosec // G404: math/rand is fine for non-security-critical delays
	return d.Min + time.Duration(rand.Int63n(int64(delta)))
}

// randomInt returns a random int between min and max (inclusive)
func randomInt(min, max int) int {
	if max <= min {
		return min
	}
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	return min + rand.Intn(max-min+1)
}

// waitWithContext waits for the specified duration or until context is cancelled
func waitWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// waitRandomWithContext waits for a random duration within the given range
func waitRandomWithContext(ctx context.Context, d config.DurationMinMax) error {
	return waitWithContext(ctx, randomDuration(d))
}
