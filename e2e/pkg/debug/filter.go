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

import (
	"strings"
	"sync"
)

// Filter is a dynamic, thread-safe log/event filter. It tracks which kinds
// and specific objects are being watched, and provides matching methods for
// both resource events and log entries.
//
// The Debugger updates this filter when Watch/Unwatch are called; log
// streamers read it concurrently to decide which entries to display.
type Filter struct {
	mu        sync.RWMutex
	kr        *KindRegistry
	byKindAll map[string]int            // kinds where all objects are watched (ref-counted)
	byName    map[string]map[string]int // kind -> name -> ref count
	allKinds  []string                  // for controller name prefix matching
}

// NewFilter creates an empty Filter using the default package-level kind
// registry. Prefer newFilter(kr) for Debugger-owned filters.
func NewFilter() *Filter {
	return newFilter(defaultKindRegistry)
}

func newFilter(kr *KindRegistry) *Filter {
	return &Filter{
		kr:        kr,
		byKindAll: make(map[string]int),
		byName:    make(map[string]map[string]int),
	}
}

// AddKind registers a kind for watching all objects of that kind.
// Multiple calls increment a reference count; the kind is only removed
// when the count drops to zero via RemoveKind.
func (f *Filter) AddKind(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byKindAll[kind]++
	f.rebuildAllKindsLocked()
}

// AddName registers a specific object (kind + name) for watching.
// Multiple calls increment a reference count; the name is only removed
// when the count drops to zero via RemoveName.
func (f *Filter) AddName(kind, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byName[kind] == nil {
		f.byName[kind] = make(map[string]int)
	}
	f.byName[kind][name]++
	f.rebuildAllKindsLocked()
}

// RemoveKind decrements the reference count for a kind-wide watch.
// The kind is only fully removed when the count reaches zero.
func (f *Filter) RemoveKind(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.byKindAll[kind]; c > 1 {
		f.byKindAll[kind] = c - 1
	} else {
		delete(f.byKindAll, kind)
	}
	f.rebuildAllKindsLocked()
}

// RemoveName decrements the reference count for a specific object watch.
// The name is only fully removed when the count reaches zero. If no more
// names remain for the kind, the kind entry is cleaned up.
func (f *Filter) RemoveName(kind, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if names, ok := f.byName[kind]; ok {
		if c := names[name]; c > 1 {
			names[name] = c - 1
		} else {
			delete(names, name)
			if len(names) == 0 {
				delete(f.byName, kind)
			}
		}
	}
	f.rebuildAllKindsLocked()
}

// MatchesObject returns true if the given kind/name should be displayed.
func (f *Filter) MatchesObject(kind, name string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.byKindAll[kind] > 0 {
		return true
	}
	if names, ok := f.byName[kind]; ok {
		return names[name] > 0
	}
	return false
}

// MatchesLog returns true if a log entry for the given controllerKind
// (full Kubernetes Kind, e.g. "ReplicatedVolume") and object name matches
// any watched target. The controllerKind is converted to a short kind via
// the kind registry before matching.
func (f *Filter) MatchesLog(controllerKind, name string) bool {
	kind := f.kr.ShortFor(controllerKind)
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.byKindAll[kind] > 0 {
		return true
	}
	if names, ok := f.byName[kind]; ok && names[name] > 0 {
		return true
	}
	return false
}

// MatchesControllerName returns true if the controller name starts with any
// watched kind prefix (e.g. controller "rvr-scheduling-controller" matches
// watched kind "rvr" because it starts with "rvr-").
func (f *Filter) MatchesControllerName(controllerName string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, kind := range f.allKinds {
		if strings.HasPrefix(controllerName, kind+"-") {
			return true
		}
	}
	return false
}

// FilterLogEntry decides whether a log entry should be displayed based on
// the current filter state. Returns true if the entry passes the filter.
func (f *Filter) FilterLogEntry(entry *LogEntry) bool {
	if entry.Msg == "Request Body" || entry.Msg == "Response Body" {
		body, exists := entry.Raw["body"]
		if !exists || body == nil || body == "" {
			return false
		}
	}

	// Has controllerKind + name → reconcile log for a specific object.
	if entry.Kind != "" && entry.Name != "" {
		if f.MatchesLog(entry.Kind, entry.Name) {
			return true
		}
		if entry.Controller != "" {
			return f.MatchesControllerName(entry.Controller)
		}
		return false
	}

	// No specific object → controller-level lifecycle message (Starting
	// EventSource, Starting Controller, shutdown, etc.). Always show.
	return true
}

// rebuildAllKindsLocked rebuilds the allKinds slice from the current maps.
// Must be called with f.mu held for writing.
func (f *Filter) rebuildAllKindsLocked() {
	seen := make(map[string]bool)
	for k := range f.byKindAll {
		seen[k] = true
	}
	for k := range f.byName {
		seen[k] = true
	}
	f.allKinds = f.allKinds[:0]
	for k := range seen {
		f.allKinds = append(f.allKinds, k)
	}
}
