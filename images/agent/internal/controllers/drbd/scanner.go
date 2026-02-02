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

package drbd

import (
	"context"
	"fmt"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// Scanner listens for DRBD events via drbdsetup events2 and triggers
// reconciliation of DRBDResource objects by sending events to the controller.
type Scanner struct {
	log     *slog.Logger
	eventCh chan event.GenericEvent
}

// NewScanner creates a new Scanner.
func NewScanner(
	log *slog.Logger,
	eventCh chan event.GenericEvent,
) *Scanner {
	return &Scanner{
		log:     log.With("name", ScannerName),
		eventCh: eventCh,
	}
}

// Start implements manager.Runnable interface.
// It starts listening for DRBD events and triggers reconciliation.
func (s *Scanner) Start(ctx context.Context) error {
	s.log.Info("Starting scanner")

	for {
		if err := s.runEventsLoop(ctx); err != nil {
			if ctx.Err() != nil {
				s.log.Info("Scanner stopping due to context cancellation")
				return nil
			}
			s.log.Error("Events loop failed, restarting", "error", err)
			// Continue to retry
		}

		select {
		case <-ctx.Done():
			return nil
		default:
			// Continue to restart
		}
	}
}

// runEventsLoop runs a single iteration of the events2 listener.
// Returns error if the loop terminates unexpectedly.
func (s *Scanner) runEventsLoop(ctx context.Context) error {
	var err error
	var online bool

	// Accumulate DRBD resource names during initial state dump to deduplicate
	pending := make(map[string]struct{})

	// processName handles a single DRBD resource name - either triggers immediately or accumulates
	processName := func(drbdName string) {
		if online {
			s.triggerReconciliation(ctx, drbdName)
		} else {
			pending[drbdName] = struct{}{}
		}
	}

	for ev := range drbdsetup.ExecuteEvents2(ctx, &err) {
		switch tev := ev.(type) {
		case *drbdsetup.Event:
			s.log.Debug("DRBD event received", "kind", tev.Kind, "object", tev.Object, "state", tev.State)

			// Check for "exists -" which indicates initial state dump is complete
			if !online && tev.Kind == "exists" && tev.Object == "-" {
				online = true
				s.log.Info("DRBD events online", "pendingResources", len(pending))
				// Trigger reconciliation for all accumulated resources
				for drbdName := range pending {
					s.triggerReconciliation(ctx, drbdName)
				}
				pending = nil // Free memory
				continue
			}

			// Process "name" field (present in most events)
			drbdName, ok := tev.State["name"]
			if !ok {
				continue
			}
			processName(drbdName)

			// For rename events, also process "new_name"
			if tev.Kind == "rename" {
				if newName, ok := tev.State["new_name"]; ok {
					processName(newName)
				}
			}

		case *drbdsetup.UnparsedEvent:
			s.log.Warn("Unparsed event", "error", tev.Err, "line", tev.RawEventLine)
		}
	}

	if err != nil {
		return fmt.Errorf("events2 failed: %w", err)
	}

	return nil
}

// triggerReconciliation sends an event to trigger reconciliation for a specific DRBD resource.
// It derives the K8S name from the DRBD name and always triggers reconciliation.
// The reconciler handles finding the actual K8S object or performing orphan cleanup.
func (s *Scanner) triggerReconciliation(ctx context.Context, drbdName string) {
	// Derive K8S name from DRBD name using helper
	k8sName, _ := ParseDRBDResourceNameOnTheNode(drbdName)

	// Create synthetic object to trigger reconciliation
	dr := &v1alpha1.DRBDResource{}
	dr.Name = k8sName

	s.sendEvent(ctx, dr)
}

// sendEvent sends a generic event to trigger reconciliation.
// Blocks until the event is sent or context is cancelled.
func (s *Scanner) sendEvent(ctx context.Context, dr *v1alpha1.DRBDResource) {
	select {
	case s.eventCh <- event.GenericEvent{Object: dr}:
		s.log.Debug("Triggered reconciliation", "name", dr.Name)
	case <-ctx.Done():
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
// Returns false because the scanner should run on all nodes, not just the leader.
func (s *Scanner) NeedLeaderElection() bool {
	return false
}
