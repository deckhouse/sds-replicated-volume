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
	gosync "sync"
	"sync/atomic"
)

// Trigger is like sync.Once with an explicit arming step.
//
// Default state is "not armed" -- DoIfEnabled is a no-op.
// EnableOnce arms the trigger; the next DoIfEnabled call fires fn exactly once.
// After fn completes, the trigger is permanently disarmed and all subsequent
// DoIfEnabled calls are lock-free no-ops.
type Trigger struct {
	enabled atomic.Bool
	mu      gosync.Mutex
	fired   bool
}

// EnableOnce arms the trigger. Only the first call has effect.
func (t *Trigger) EnableOnce() {
	t.enabled.Store(true)
}

// DoIfEnabled calls fn exactly once if the trigger has been armed via EnableOnce.
// If not armed, returns nil immediately (lock-free fast path).
// If armed, acquires a mutex and re-checks (double-check pattern) to ensure
// fn is called at most once. After fn returns, the trigger is permanently
// disarmed. Concurrent callers block on the mutex until fn completes, then
// return nil.
func (t *Trigger) DoIfEnabled(fn func() error) error {
	if !t.enabled.Load() {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.fired {
		return nil
	}

	err := fn()
	t.fired = true
	t.enabled.Store(false)
	return err
}
