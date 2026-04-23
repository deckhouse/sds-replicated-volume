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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

const (
	scannerRetryBaseDelay = 1 * time.Second
	scannerRetryMaxDelay  = 30 * time.Second
)

// Scanner listens for DRBD events via drbdutils events2 and triggers
// reconciliation of DRBDResource objects by sending events to the controller.
// It maintains a DRBDPortCache with port information from path events and
// a DRBDStateStore with full runtime state for reconciler reads.
type Scanner struct {
	requestCh  chan<- event.TypedGenericEvent[DRBDReconcileRequest]
	portCache  *DRBDPortCache
	stateStore *DRBDStateStore
}

// NewScanner creates a new Scanner.
func NewScanner(
	requestCh chan<- event.TypedGenericEvent[DRBDReconcileRequest],
	portCache *DRBDPortCache,
	stateStore *DRBDStateStore,
) *Scanner {
	return &Scanner{
		requestCh:  requestCh,
		portCache:  portCache,
		stateStore: stateStore,
	}
}

// Start implements manager.Runnable interface.
// It starts listening for DRBD events and triggers reconciliation.
func (s *Scanner) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName(ScannerName)
	ctx = log.IntoContext(ctx, logger)

	logger.Info("Starting scanner")

	retryDelay := scannerRetryBaseDelay
	for {
		if err := s.runEventsLoop(ctx); err != nil {
			if ctx.Err() != nil {
				logger.Info("Scanner stopping due to context cancellation")
				return nil
			}
			logger.Error(err, "Events loop failed, retrying", "retryDelay", retryDelay)

			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return nil
			}

			retryDelay = min(retryDelay*2, scannerRetryMaxDelay)
			continue
		}

		retryDelay = scannerRetryBaseDelay

		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

// runEventsLoop runs a single iteration of the events2 listener.
// Returns error if the loop terminates unexpectedly.
func (s *Scanner) runEventsLoop(ctx context.Context) error {
	s.portCache.BeginDump()
	s.stateStore.BeginDump()
	dumpCompleted := false
	defer func() {
		if !dumpCompleted {
			s.portCache.AbortDump()
			s.stateStore.AbortDump()
		}
	}()

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

	for ev := range drbdutils.ExecuteEvents2(ctx, &err) {
		switch tev := ev.(type) {
		case *drbdutils.Event:
			logger.V(1).Info("DRBD event received", "kind", tev.Kind, "object", tev.Object, "state", tev.State)

			// Check for "exists -" which indicates initial state dump is complete
			if !online && tev.Kind == drbdutils.EventKindExists && tev.Object == drbdutils.EventObjectDumpDone {
				s.portCache.EndDump()
				s.stateStore.EndDump()
				dumpCompleted = true
				online = true
				logger.Info("DRBD events online", "pendingResources", len(pending))
				// Trigger reconciliation for all accumulated resources
				for drbdName := range pending {
					s.triggerReconciliation(ctx, drbdName)
				}
				pending = nil // Free memory
				continue
			}

			if err := s.stateStore.ApplyEvent(tev); err != nil {
				logger.Error(err, "Failed to apply event to state store", "kind", tev.Kind, "object", tev.Object)
			}

			// Process "name" field (present in most events)
			drbdName, ok := tev.State["name"]
			if !ok {
				continue
			}
			processName(drbdName)

			s.updatePortCache(tev, drbdName)

			// For rename events, also process "new_name"
			if tev.Kind == drbdutils.EventKindRename {
				if newName, ok := tev.State["new_name"]; ok {
					processName(newName)
				}
			}

		case *drbdutils.UnparsedEvent:
			logger.Error(tev.Err, "Unparsed DRBD event", "line", tev.RawEventLine)
		}
	}

	if err != nil {
		return fmt.Errorf("events2 failed: %w", err)
	}

	return nil
}

// updatePortCache updates the DRBDPortCache based on path and resource events.
func (s *Scanner) updatePortCache(ev *drbdutils.Event, drbdName string) {
	switch ev.Object {
	case drbdutils.EventObjectPath:
		local, ok := ev.State["local"]
		if !ok {
			return
		}
		ip, port, ok := parseLocalAddr(local)
		if !ok {
			return
		}
		switch ev.Kind {
		case drbdutils.EventKindExists, drbdutils.EventKindCreate:
			s.portCache.Add(drbdName, ip, port)
		case drbdutils.EventKindDestroy:
			s.portCache.Remove(drbdName, ip, port)
		}
	case drbdutils.EventObjectResource:
		if ev.Kind == drbdutils.EventKindDestroy {
			s.portCache.RemoveResource(drbdName)
		}
	}
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
