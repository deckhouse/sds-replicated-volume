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

package drbdm

import (
	"context"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/udev"
)

const (
	scannerRetryBaseDelay = 1 * time.Second
	scannerRetryMaxDelay  = 30 * time.Second
)

// Scanner listens for udev events related to device-mapper devices and triggers
// reconciliation of DRBDMapper objects by sending events to the controller.
type Scanner struct {
	requestCh chan<- event.TypedGenericEvent[reconcile.Request]
}

// NewScanner creates a new Scanner.
func NewScanner(
	requestCh chan<- event.TypedGenericEvent[reconcile.Request],
) *Scanner {
	return &Scanner{
		requestCh: requestCh,
	}
}

// Start implements manager.Runnable interface.
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

// runEventsLoop runs a single iteration of the udev monitor listener.
func (s *Scanner) runEventsLoop(ctx context.Context) error {
	eventCh := make(chan udev.BlockEvent, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- udev.MonitorBlockEvents(ctx, eventCh)
	}()

	logger := log.FromContext(ctx)
	logger.Info("Connected to udev netlink, listening for block events")

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case ev := <-eventCh:
			dmName := ev.Env["DM_NAME"]
			if dmName == "" {
				continue
			}
			crName := strings.TrimSuffix(dmName, internalDeviceSuffix)
			logger.V(1).Info("Udev block event for DM device", "dmName", dmName, "crName", crName, "action", ev.Action)
			s.triggerReconciliation(ctx, crName)
		}
	}
}

func (s *Scanner) triggerReconciliation(ctx context.Context, name string) {
	logger := log.FromContext(ctx)

	req := reconcile.Request{}
	req.Name = name
	logger.V(1).Info("Triggered reconciliation", "name", name)

	select {
	case s.requestCh <- event.TypedGenericEvent[reconcile.Request]{Object: req}:
	case <-ctx.Done():
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
// Returns false because the scanner should run on all nodes, not just the leader.
func (s *Scanner) NeedLeaderElection() bool {
	return false
}
