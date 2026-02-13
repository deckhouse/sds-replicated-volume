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

package drbdr

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// Scanner listens for DRBD events via drbdsetup events2 and triggers
// reconciliation of DRBDResource objects by sending events to the controller.
type Scanner struct {
	requestCh chan<- event.TypedGenericEvent[DRBDReconcileRequest]
}

// NewScanner creates a new Scanner.
func NewScanner(
	requestCh chan<- event.TypedGenericEvent[DRBDReconcileRequest],
) *Scanner {
	return &Scanner{
		requestCh: requestCh,
	}
}

// Start implements manager.Runnable interface.
// It starts listening for DRBD events and triggers reconciliation.
func (s *Scanner) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName(ScannerName)
	ctx = log.IntoContext(ctx, logger)

	logger.Info("Starting scanner")

	for {
		if err := s.runEventsLoop(ctx); err != nil {
			if ctx.Err() != nil {
				logger.Info("Scanner stopping due to context cancellation")
				return nil
			}
			logger.Error(err, "Events loop failed, restarting")
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

	logger := log.FromContext(ctx)

	for ev := range drbdsetup.ExecuteEvents2(ctx, &err) {
		switch tev := ev.(type) {
		case *drbdsetup.Event:
			logger.V(1).Info("DRBD event received", "kind", tev.Kind, "object", tev.Object, "state", tev.State)

			// Check for "exists -" which indicates initial state dump is complete
			if !online && tev.Kind == "exists" && tev.Object == "-" {
				online = true
				logger.Info("DRBD events online", "pendingResources", len(pending))
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
			logger.Info("Unparsed event", "error", tev.Err, "line", tev.RawEventLine)
		}
	}

	if err != nil {
		return fmt.Errorf("events2 failed: %w", err)
	}

	return nil
}

// triggerReconciliation sends a request to trigger reconciliation for a specific DRBD resource.
// If the DRBD name has the standard "sdsrv-" prefix, it derives the K8S name and sends a Name-based request.
// If the DRBD name does not have the prefix, it sends an ActualNameOnTheNode-based request for orphan/rename handling.
func (s *Scanner) triggerReconciliation(ctx context.Context, drbdName string) {
	logger := log.FromContext(ctx)
	var req DRBDReconcileRequest

	// If DRBD name has standard prefix, we can derive K8S name
	if k8sName, hasPrefix := ParseDRBDResourceNameOnTheNode(drbdName); hasPrefix {
		req.Name = k8sName
		logger.V(1).Info("Triggered reconciliation (by k8s name)", "name", k8sName)
	} else {
		// No prefix - use ActualNameOnTheNode for orphan/rename handling
		req.ActualNameOnTheNode = drbdName
		logger.V(1).Info("Triggered reconciliation (by actual name)", "actualNameOnTheNode", drbdName)
	}

	select {
	case s.requestCh <- event.TypedGenericEvent[DRBDReconcileRequest]{Object: req}:
	case <-ctx.Done():
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
// Returns false because the scanner should run on all nodes, not just the leader.
func (s *Scanner) NeedLeaderElection() bool {
	return false
}
