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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// Scanner listens for DRBD events via drbdsetup events2 and triggers
// reconciliation of DRBDResource objects by sending events to the controller.
type Scanner struct {
	log      *slog.Logger
	cl       client.Client
	nodeName string
	eventCh  chan event.GenericEvent
}

// NewScanner creates a new Scanner.
func NewScanner(
	cl client.Client,
	log *slog.Logger,
	nodeName string,
	eventCh chan event.GenericEvent,
) *Scanner {
	return &Scanner{
		log:      log.With("name", ScannerName),
		cl:       cl,
		nodeName: nodeName,
		eventCh:  eventCh,
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

	for ev := range drbdsetup.ExecuteEvents2(ctx, &err) {
		switch tev := ev.(type) {
		case *drbdsetup.Event:
			// Check for "exists -" which indicates initial state dump is complete
			if !online && tev.Kind == "exists" && tev.Object == "-" {
				online = true
				s.log.Info("DRBD events online, triggering full reconciliation")
				// Trigger reconciliation for all resources on this node
				s.triggerAllResources(ctx)
				continue
			}

			// Extract resource name from event
			resourceName, ok := tev.State["name"]
			if !ok {
				s.log.Debug("Skipping event without name", "event", tev)
				continue
			}

			s.log.Debug("DRBD event received", "kind", tev.Kind, "object", tev.Object, "resource", resourceName)
			s.triggerReconciliation(ctx, resourceName)

		case *drbdsetup.UnparsedEvent:
			s.log.Warn("Unparsed event", "error", tev.Err, "line", tev.RawEventLine)
		}
	}

	if err != nil {
		return fmt.Errorf("events2 failed: %w", err)
	}

	return nil
}

// triggerAllResources lists all DRBDResource objects for this node and triggers reconciliation.
func (s *Scanner) triggerAllResources(ctx context.Context) {
	drList := &v1alpha1.DRBDResourceList{}
	if err := s.cl.List(ctx, drList); err != nil {
		s.log.Error("Failed to list DRBDResources", "error", err)
		return
	}

	for i := range drList.Items {
		dr := &drList.Items[i]
		if dr.Spec.NodeName != s.nodeName {
			continue
		}

		s.sendEvent(ctx, dr)
	}
}

// triggerReconciliation sends an event to trigger reconciliation for a specific resource.
func (s *Scanner) triggerReconciliation(ctx context.Context, resourceName string) {
	resourceName, nameFormatValid := v1alpha1.ParseDRBDResourceNameOnTheNode(resourceName)
	if !nameFormatValid {
		s.log.Debug("Resource has invalid name format, will be searching by ActualNameOnTheNode", "resourceName", resourceName)
	}

	// List DRBDResources to find the one matching the resource name
	// The DRBD resource name in the API is derived from the DRBDResource.Name
	drList := &v1alpha1.DRBDResourceList{}
	if err := s.cl.List(ctx, drList); err != nil {
		s.log.Error("Failed to list DRBDResources", "error", err)
		return
	}

	for i := range drList.Items {
		dr := &drList.Items[i]
		if dr.Spec.NodeName != s.nodeName {
			continue
		}

		if dr.Spec.ActualNameOnTheNode == resourceName || (nameFormatValid && dr.Name == resourceName) {
			s.sendEvent(ctx, dr)
			return
		}
	}

	// If not found by exact name, trigger all resources (fallback for initial implementation)
	s.log.Warn("Resource not found by name", "resourceName", resourceName)
}

// sendEvent sends a generic event to trigger reconciliation.
func (s *Scanner) sendEvent(ctx context.Context, dr *v1alpha1.DRBDResource) {
	select {
	case s.eventCh <- event.GenericEvent{Object: dr}:
		s.log.Debug("Triggered reconciliation", "name", dr.Name)
	case <-ctx.Done():
		return
	default:
		// Channel full, skip this resource this time
		s.log.Warn("Event channel full, skipping resource", "name", dr.Name)
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
// Returns false because the scanner should run on all nodes, not just the leader.
func (s *Scanner) NeedLeaderElection() bool {
	return false
}
