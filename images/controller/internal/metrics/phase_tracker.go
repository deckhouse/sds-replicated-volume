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

package metrics

import (
	"sync"
	"time"
)

// PhaseEntry tracks when an RVR entered its current phase.
type PhaseEntry struct {
	Phase     string
	StartTime time.Time
}

// PhaseTracker maintains an in-process map for RVR phase duration tracking.
// On controller restart the map is empty; the first reconcile for each RVR
// initializes the entry with ControllerStartTime as fallback (see GetOrInit).
// This ensures deadlock detection works immediately after restart, at the cost
// of a one-time underestimated phase duration in the histogram.
//
// Memory: ~150 bytes per entry. At 1500 RVRs = ~220 KB. Zero I/O overhead.
type PhaseTracker struct {
	entries          sync.Map // map[string]PhaseEntry (key = RVR name)
	creationObserved sync.Map // map[string]struct{} — names that had creation duration observed
}

// RVRPhases is the package-level PhaseTracker instance for RVR phase tracking.
var RVRPhases = &PhaseTracker{}

// GetOrInit returns the existing entry for the given RVR name.
// If no entry exists, initializes one with the given phase and fallbackStartTime
// (typically ControllerStartTime for graceful degradation after restart).
func (pt *PhaseTracker) GetOrInit(name, phase string, fallbackStartTime time.Time) PhaseEntry {
	entry := PhaseEntry{Phase: phase, StartTime: fallbackStartTime}
	if actual, loaded := pt.entries.LoadOrStore(name, entry); loaded {
		return actual.(PhaseEntry)
	}
	return entry
}

// RecordPhaseChange records a phase transition for the given RVR.
// Returns the previous entry and its duration. If no previous entry existed,
// existed is false.
func (pt *PhaseTracker) RecordPhaseChange(name, newPhase string) (prev PhaseEntry, duration time.Duration, existed bool) {
	now := time.Now()
	newEntry := PhaseEntry{Phase: newPhase, StartTime: now}
	if old, loaded := pt.entries.Swap(name, newEntry); loaded {
		prev = old.(PhaseEntry)
		return prev, now.Sub(prev.StartTime), true
	}
	return PhaseEntry{}, 0, false
}

// IsCreationObserved returns true if creation duration was already observed for this RVR.
func (pt *PhaseTracker) IsCreationObserved(name string) bool {
	_, loaded := pt.creationObserved.Load(name)
	return loaded
}

// MarkCreationObserved marks that creation duration was observed for this RVR.
// Prevents double-counting on Healthy -> Degraded -> Healthy oscillations.
func (pt *PhaseTracker) MarkCreationObserved(name string) {
	pt.creationObserved.Store(name, struct{}{})
}

// Delete removes the entry for the given RVR name.
// Must be called when the RVR is deleted to prevent memory leaks.
func (pt *PhaseTracker) Delete(name string) {
	pt.entries.Delete(name)
	pt.creationObserved.Delete(name)
}
