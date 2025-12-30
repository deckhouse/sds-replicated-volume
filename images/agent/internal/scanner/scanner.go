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

package scanner

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/cooldown"
	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type ResourceScanner interface {
	ResourceShouldBeRefreshed(resourceName string)
}

var defaultScanner atomic.Pointer[ResourceScanner]

func DefaultScanner() *ResourceScanner {
	return defaultScanner.Load()
}

func SetDefaultScanner(s ResourceScanner) {
	defaultScanner.Store(&s)
}

type Scanner struct {
	log      *slog.Logger
	hostname string
	ctx      context.Context
	cancel   context.CancelCauseFunc
	batcher  *cooldown.BatcherTyped[updatedResourceName]
	cl       client.Client
}

func NewScanner(
	ctx context.Context,
	log *slog.Logger,
	cl client.Client,
	hostname string,
) *Scanner {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &Scanner{
		hostname: hostname,
		ctx:      ctx,
		cancel:   cancel,
		log:      log,
		cl:       cl,
		batcher:  cooldown.NewBatcher(appendUpdatedResourceNameToBatch),
	}
	return s
}

func (s *Scanner) retryUntilCancel(fn func() error) error {
	return retry.OnError(
		wait.Backoff{
			Steps:    8,
			Duration: 50 * time.Millisecond,
			Factor:   2.0,
			Cap:      5 * time.Second,
			Jitter:   0.1,
		},
		func(_ error) bool {
			// retry any error until parent context is done
			return s.ctx.Err() == nil
		},
		fn,
	)
}

func (s *Scanner) ResourceShouldBeRefreshed(resourceName string) {
	_ = s.batcher.Add(updatedResourceName(resourceName))
}

func (s *Scanner) Run() error {
	return s.retryUntilCancel(func() error {
		var err error

		for ev := range s.processEvents(drbdsetup.ExecuteEvents2(s.ctx, &err)) {
			s.log.Debug("added resource update event", "resource", ev)
			if err := s.batcher.Add(ev); err != nil {
				return u.LogError(s.log, fmt.Errorf("adding event to batcher: %w", err))
			}
		}

		if err != nil && s.ctx.Err() == nil {
			return u.LogError(s.log, fmt.Errorf("run events2: %w", err))
		}

		if err != nil && s.ctx.Err() != nil {
			// err likely caused by context cancelation, so it's not critical
			s.log.Warn(fmt.Sprintf("run events2: %v", err))
		}

		return s.ctx.Err()
	})
}

type updatedResourceName string

func appendUpdatedResourceNameToBatch(batch []updatedResourceName, newItem updatedResourceName) []updatedResourceName {
	if !slices.Contains(batch, newItem) {
		return append(batch, newItem)
	}
	return batch
}

func (s *Scanner) processEvents(
	allEvents iter.Seq[drbdsetup.Events2Result],
) iter.Seq[updatedResourceName] {
	return func(yield func(updatedResourceName) bool) {
		var online bool
		for ev := range allEvents {
			var typedEvent *drbdsetup.Event

			switch tev := ev.(type) {
			case *drbdsetup.Event:
				typedEvent = tev
			case *drbdsetup.UnparsedEvent:
				s.log.Warn(
					"unparsed event",
					"err", tev.Err,
					"line", tev.RawEventLine,
				)
				continue
			default:
				s.log.Error(
					"unexpected event type",
					"event", fmt.Sprintf("%v", tev),
				)
				continue
			}

			if !online &&
				typedEvent.Kind == "exists" &&
				typedEvent.Object == "-" {
				online = true
				s.log.Debug("events online")
			}

			resourceName, ok := typedEvent.State["name"]
			if !ok {
				s.log.Debug("skipping event without name")
				continue
			}
			s.log.Debug("yielding event", "event", typedEvent)
			if !yield(updatedResourceName(resourceName)) {
				return
			}
		}
	}
}

func (s *Scanner) ConsumeBatches() error {
	// Create cooldown OUTSIDE the retry loop to preserve its state across retries
	cd := cooldown.NewExponentialCooldown(
		1*time.Second,
		5*time.Second,
	)
	log := s.log.With("goroutine", "consumeBatches")

	return s.retryUntilCancel(func() error {
		for batch := range s.batcher.ConsumeWithCooldown(s.ctx, cd) {
			log.Debug("got batch of 'n' resources", "n", len(batch))

			statusResult, err := drbdsetup.ExecuteStatus(s.ctx)
			if err != nil {
				return u.LogError(log, fmt.Errorf("getting statusResult: %w", err))
			}
			resourceStatusByName := make(map[string]*drbdsetup.Resource, len(statusResult))
			for i := range statusResult {
				resourceStatusByName[statusResult[i].Name] = &statusResult[i]
			}

			log.Debug("got status for 'n' resources", "n", len(statusResult))

			var batchErrors error
			for _, item := range batch {
				resourceName := string(item)

				resourceStatus, ok := resourceStatusByName[resourceName]
				if !ok {
					log.Warn(
						"got update event for resource 'resourceName', but it's missing in drbdsetup status",
						"resourceName", resourceName,
					)
					continue
				}

				if err := s.refreshResource(log, resourceStatus); err != nil {
					batchErrors = errors.Join(batchErrors, err)
					// requeue same item
					_ = s.batcher.Add(item)
				}
			}

			if batchErrors != nil {
				return batchErrors
			}
		}

		return s.ctx.Err()
	})
}

func (s *Scanner) refreshResource(log *slog.Logger, resourceStatus *drbdsetup.Resource) error {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			// required for SetNameWithNodeID
			ReplicatedVolumeName: resourceStatus.Name,
		},
	}
	rvr.SetNameWithNodeID(uint(resourceStatus.NodeID))
	if err := s.cl.Get(s.ctx, client.ObjectKeyFromObject(rvr), rvr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Warn(
				"got update event for resource 'resourceName' nodeId='nodeId', but rvr 'rvrName' missing in cluster",
				"resourceName", resourceStatus.Name,
				"nodeId", resourceStatus.NodeID,
				"rvrName", rvr.Name,
			)
			return nil
		}
		return u.LogError(log, fmt.Errorf("getting rvr for resource: %w", err))
	}

	if rvr.Spec.NodeName != s.hostname {
		log.Error(
			"got update event for rvr 'rvrNodeName', but it has unexpected node name",
			"hostname", s.hostname,
			"rvrNodeName", rvr.Spec.NodeName,
		)
		return nil
	}

	err := s.updateReplicaStatusIfNeeded(rvr, resourceStatus)
	if err != nil {
		return u.LogError(log, fmt.Errorf("updating replica status: %w", err))
	}
	log.Debug("updated replica status", "resourceName", resourceStatus.Name)
	return nil
}

func (s *Scanner) updateReplicaStatusIfNeeded(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) error {
	statusPatch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}
	if rvr.Status.DRBD.Status == nil {
		rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
	}
	copyStatusFields(rvr.Status.DRBD.Status, resource)

	_ = rvr.UpdateStatusConditionDataInitialized()
	_ = rvr.UpdateStatusConditionInQuorum()
	_ = rvr.UpdateStatusConditionInSync()

	// Calculate SyncProgress for kubectl display
	rvr.Status.SyncProgress = calculateSyncProgress(rvr, resource)

	if err := s.cl.Status().Patch(s.ctx, rvr, statusPatch); err != nil {
		return fmt.Errorf("patching status: %w", err)
	}

	return nil
}

// calculateSyncProgress returns a string for the SyncProgress field:
// - "True" when InSync condition is True
// - "Unknown" when InSync condition is Unknown or not set
// - "XX.XX%" during active synchronization (when this replica is SyncTarget)
// - DiskState (e.g. "Outdated") when not syncing but not in sync
func calculateSyncProgress(rvr *v1alpha1.ReplicatedVolumeReplica, resource *drbdsetup.Resource) string {
	// Check InSync condition first
	inSyncCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeInSync)
	if inSyncCond != nil && inSyncCond.Status == metav1.ConditionTrue {
		return "True"
	}

	// Return Unknown if condition is not yet set or explicitly Unknown
	if inSyncCond == nil || inSyncCond.Status == metav1.ConditionUnknown {
		return "Unknown"
	}

	// Get local disk state
	if len(resource.Devices) == 0 {
		return "Unknown"
	}
	localDiskState := resource.Devices[0].DiskState

	// Check if we are SyncTarget - find minimum PercentInSync from connections
	// where replication state indicates active sync
	var minPercent float64 = -1
	for _, conn := range resource.Connections {
		for _, pd := range conn.PeerDevices {
			if isSyncingState(pd.ReplicationState) {
				if minPercent < 0 || pd.PercentInSync < minPercent {
					minPercent = pd.PercentInSync
				}
			}
		}
	}

	// If we found active sync, return the percentage
	if minPercent >= 0 {
		return fmt.Sprintf("%.2f%%", minPercent)
	}

	// Not syncing - return disk state
	return localDiskState
}

// isSyncingState returns true if the replication state indicates active synchronization
func isSyncingState(state string) bool {
	switch state {
	case "SyncSource", "SyncTarget",
		"StartingSyncS", "StartingSyncT",
		"PausedSyncS", "PausedSyncT",
		"WFBitMapS", "WFBitMapT",
		"WFSyncUUID":
		return true
	default:
		return false
	}
}

func copyStatusFields(
	target *v1alpha1.DRBDStatus,
	source *drbdsetup.Resource,
) {
	// Some properties were removed, as they are too verbose. See "removed (verbose):"

	target.Name = source.Name
	target.NodeId = source.NodeID
	target.Role = source.Role
	target.Suspended = source.Suspended
	target.SuspendedUser = source.SuspendedUser
	target.SuspendedNoData = source.SuspendedNoData
	target.SuspendedFencing = source.SuspendedFencing
	target.SuspendedQuorum = source.SuspendedQuorum
	target.ForceIOFailures = source.ForceIOFailures
	target.WriteOrdering = source.WriteOrdering

	// Devices
	target.Devices = make([]v1alpha1.DeviceStatus, 0, len(source.Devices))
	for _, d := range source.Devices {
		target.Devices = append(target.Devices, v1alpha1.DeviceStatus{
			Volume:    d.Volume,
			Minor:     d.Minor,
			DiskState: v1alpha1.ParseDiskState(d.DiskState),
			Client:    d.Client,
			Open:      d.Open,
			Quorum:    d.Quorum,
			Size:      d.Size,
			// removed (verbose): Read:         d.Read,
			// removed (verbose): Written:      d.Written,
			// removed (verbose): ALWrites:     d.ALWrites,
			// removed (verbose): BMWrites:     d.BMWrites,
			// removed (verbose): UpperPending: d.UpperPending,
			// removed (verbose): LowerPending: d.LowerPending,
		})
	}

	// Connections
	target.Connections = make([]v1alpha1.ConnectionStatus, 0, len(source.Connections))
	for _, c := range source.Connections {
		conn := v1alpha1.ConnectionStatus{
			PeerNodeId:      c.PeerNodeID,
			Name:            c.Name,
			ConnectionState: v1alpha1.ParseConnectionState(c.ConnectionState),
			Congested:       c.Congested,
			Peerrole:        c.Peerrole,
			TLS:             c.TLS,
			// removed (verbose): APInFlight:      c.APInFlight,
			// removed (verbose): RSInFlight:      c.RSInFlight,
		}

		// Paths
		conn.Paths = make([]v1alpha1.PathStatus, 0, len(c.Paths))
		for _, p := range c.Paths {
			conn.Paths = append(conn.Paths, v1alpha1.PathStatus{
				ThisHost: v1alpha1.HostStatus{
					Address: p.ThisHost.Address,
					Port:    p.ThisHost.Port,
					Family:  p.ThisHost.Family,
				},
				RemoteHost: v1alpha1.HostStatus{
					Address: p.RemoteHost.Address,
					Port:    p.RemoteHost.Port,
					Family:  p.RemoteHost.Family,
				},
				Established: p.Established,
			})
		}

		// Peer devices
		conn.PeerDevices = make([]v1alpha1.PeerDeviceStatus, 0, len(c.PeerDevices))
		for _, pd := range c.PeerDevices {
			conn.PeerDevices = append(conn.PeerDevices, v1alpha1.PeerDeviceStatus{
				Volume:           pd.Volume,
				ReplicationState: v1alpha1.ParseReplicationState(pd.ReplicationState),
				PeerDiskState:    v1alpha1.ParseDiskState(pd.PeerDiskState),
				PeerClient:       pd.PeerClient,
				ResyncSuspended:  pd.ResyncSuspended,
				OutOfSync:        pd.OutOfSync,
				// removed (verbose): Pending:                pd.Pending,
				// removed (verbose): Unacked:                pd.Unacked,
				HasSyncDetails:         pd.HasSyncDetails,
				HasOnlineVerifyDetails: pd.HasOnlineVerifyDetails,
				// removed (verbose): PercentInSync:          fmt.Sprintf("%.2f", pd.PercentInSync),
			})
		}

		target.Connections = append(target.Connections, conn)
	}
}
