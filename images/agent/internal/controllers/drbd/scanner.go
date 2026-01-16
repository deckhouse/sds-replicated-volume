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
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Scanner periodically triggers controller reconciliation for all DRBDResource
// objects on this node by sending events through a channel.
type Scanner struct {
	log      *slog.Logger
	cl       client.Client
	nodeName string
	interval time.Duration
	eventCh  chan event.GenericEvent
}

// NewScanner creates a new Scanner.
func NewScanner(
	cl client.Client,
	log *slog.Logger,
	nodeName string,
	interval time.Duration,
	eventCh chan event.GenericEvent,
) *Scanner {
	return &Scanner{
		log:      log.With("name", ScannerName),
		cl:       cl,
		nodeName: nodeName,
		interval: interval,
		eventCh:  eventCh,
	}
}

// Start implements manager.Runnable interface.
// It starts the periodic scanning loop.
func (s *Scanner) Start(ctx context.Context) error {
	s.log.Info("Starting scanner", "interval", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on start
	s.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("Scanner stopping due to context cancellation")
			return nil
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

// scan lists all DRBDResource objects for this node and sends events to trigger reconciliation.
func (s *Scanner) scan(ctx context.Context) {
	s.log.Debug("Starting periodic scan")

	// List all DRBDResource objects
	drList := &v1alpha1.DRBDResourceList{}
	if err := s.cl.List(ctx, drList); err != nil {
		s.log.Error("Failed to list DRBDResources", "error", err)
		return
	}

	enqueued := 0
	for i := range drList.Items {
		dr := &drList.Items[i]
		// Only enqueue resources for this node
		if dr.Spec.NodeName != s.nodeName {
			continue
		}

		// Send event through channel to trigger reconciliation
		select {
		case s.eventCh <- event.GenericEvent{Object: dr}:
			enqueued++
		case <-ctx.Done():
			return
		default:
			// Channel full, skip this resource this time
			s.log.Warn("Event channel full, skipping resource", "name", dr.Name)
		}
	}

	s.log.Debug("Periodic scan completed", "enqueued", enqueued)
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
// Returns false because the scanner should run on all nodes, not just the leader.
func (s *Scanner) NeedLeaderElection() bool {
	return false
}
