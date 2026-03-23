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

package debug

import "sync"

// objectState stores the previous snapshot of a single Kubernetes object for
// diffing: the cleaned YAML lines (without conditions) and the last-seen
// conditions list.
type objectState struct {
	Lines      []string
	Conditions []Condition
}

// stateKey identifies an object in the tracker by GVK short name and name.
type stateKey struct {
	GVK  string // e.g. "rv", "rvr", "rsc"
	Name string
}

// stateTracker is a concurrent-safe per-object state map.
type stateTracker struct {
	mu    sync.RWMutex
	state map[stateKey]*objectState
}

func newStateTracker() *stateTracker {
	return &stateTracker{
		state: make(map[stateKey]*objectState),
	}
}

// Get returns the stored state for the given key, or nil if not present.
func (t *stateTracker) Get(key stateKey) *objectState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state[key]
}

// Set stores (or replaces) the state for the given key.
func (t *stateTracker) Set(key stateKey, s *objectState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state[key] = s
}

// Delete removes the state for the given key.
func (t *stateTracker) Delete(key stateKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.state, key)
}

// deleteByGVK removes all state entries matching the given GVK short name.
// Unexported test affordance — not used by production code paths.
func (t *stateTracker) deleteByGVK(gvk string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.state {
		if k.GVK == gvk {
			delete(t.state, k)
		}
	}
}

// Clear removes all stored state.
func (t *stateTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = make(map[stateKey]*objectState)
}

// count returns the number of tracked objects.
// Unexported test affordance — not used by production code paths.
func (t *stateTracker) count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.state)
}
