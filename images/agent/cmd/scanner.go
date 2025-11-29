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

package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/cooldown"
	. "github.com/deckhouse/sds-common-lib/utils"
	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/common/lang"
)

type scanner struct {
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
	envConfig *EnvConfig,
) *scanner {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &scanner{
		hostname: envConfig.NodeName,
		ctx:      ctx,
		cancel:   cancel,
		log:      log,
		cl:       cl,
		batcher:  cooldown.NewBatcher(appendUpdatedResourceNameToBatch),
	}
	return s
}

func (s *scanner) retryUntilCancel(fn func() error) error {
	return retry.OnError(
		wait.Backoff{
			Steps:    7,
			Duration: 50 * time.Millisecond,
			Factor:   2.0,
			Cap:      5 * time.Second,
			Jitter:   0.1,
		},
		func(err error) bool {
			// retry any error until parent context is done
			return s.ctx.Err() == nil
		},
		fn,
	)
}

func (s *scanner) Run() error {
	return s.retryUntilCancel(func() error {
		var err error

		for ev := range s.processEvents(drbdsetup.ExecuteEvents2(s.ctx, &err)) {
			s.log.Debug("added resource update event", "resource", ev)
			if err := s.batcher.Add(ev); err != nil {
				return LogError(s.log, fmt.Errorf("adding event to batcher: %w", err))
			}
		}

		if err != nil && s.ctx.Err() == nil {
			return LogError(s.log, fmt.Errorf("run events2: %w", err))
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
	if !slices.ContainsFunc(
		batch,
		func(e updatedResourceName) bool { return e == newItem },
	) {
		return append(batch, newItem)
	}

	return batch
}

func (s *scanner) processEvents(
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

			if resourceName, ok := typedEvent.State["name"]; !ok {
				s.log.Debug("skipping event without name")
				continue
			} else {
				s.log.Debug("yielding event", "event", typedEvent)
				if !yield(updatedResourceName(resourceName)) {
					return
				}
			}
		}
	}
}

func (s *scanner) ConsumeBatches() error {
	return s.retryUntilCancel(func() error {
		cd := cooldown.NewExponentialCooldown(
			50*time.Millisecond,
			5*time.Second,
		)
		log := s.log.With("goroutine", "consumeBatches")

		for batch := range s.batcher.ConsumeWithCooldown(s.ctx, cd) {
			log.Debug("got batch of 'n' resources", "n", len(batch))

			statusResult, err := drbdsetup.ExecuteStatus(s.ctx)
			if err != nil {
				return LogError(log, fmt.Errorf("getting statusResult: %w", err))
			}

			log.Debug("got status for 'n' resources", "n", len(statusResult))

			rvrList := &v1alpha2.ReplicatedVolumeReplicaList{}

			// we expect this query to hit cache with index
			err = s.cl.List(
				s.ctx,
				rvrList,
				client.MatchingFieldsSelector{
					Selector: (&v1alpha2.ReplicatedVolumeReplica{}).
						NodeNameSelector(s.hostname),
				},
			)
			if err != nil {
				return LogError(log, fmt.Errorf("listing rvr: %w", err))
			}

			for _, item := range batch {
				resourceName := string(item)

				resourceStatus, ok := uiter.Find(
					uslices.Ptrs(statusResult),
					func(res *drbdsetup.Resource) bool { return res.Name == resourceName },
				)
				if !ok {
					log.Warn(
						"got update event for resource 'resourceName', but it's missing in drbdsetup status",
						"resourceName", resourceName,
					)
					continue
				}

				rvr, ok := uiter.Find(
					uslices.Ptrs(rvrList.Items),
					func(rvr *v1alpha2.ReplicatedVolumeReplica) bool {
						return rvr.Spec.ReplicatedVolumeName == resourceName && rvr.IsConfigured()
					},
				)
				if !ok {
					log.Debug(
						"didn't find rvr with 'replicatedVolumeName'",
						"replicatedVolumeName", resourceName,
					)
					continue
				}

				err := s.updateReplicaStatusIfNeeded(rvr, resourceStatus)
				if err != nil {
					return LogError(
						log,
						fmt.Errorf("updating replica status: %w", err),
					)
				}
				log.Debug("updated replica status", "resourceName", resourceName)
			}
		}

		return s.ctx.Err()
	})
}

func (s *scanner) updateReplicaStatusIfNeeded(
	rvr *v1alpha2.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) error {
	return api.PatchStatusWithConflictRetry(
		s.ctx,
		s.cl,
		rvr,
		func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
			rvr.InitializeStatusConditions()
			if rvr.Status.DRBD == nil {
				rvr.Status.DRBD = &v1alpha2.DRBDStatus{}
			}
			copyStatusFields(rvr.Status.DRBD, resource)

			diskless, err := rvr.Status.Config.Diskless()
			if err != nil {
				return err
			}

			devicesIter := uslices.Ptrs(resource.Devices)

			failedDevice, foundFailed := uiter.Find(
				devicesIter,
				func(d *drbdsetup.Device) bool {
					if diskless {
						return d.DiskState != "Diskless"
					} else {
						return d.DiskState != "UpToDate"
					}
				},
			)

			allReady := !foundFailed && len(resource.Devices) > 0

			if allReady && !meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync) {
				meta.SetStatusCondition(
					&rvr.Status.Conditions,
					metav1.Condition{
						Type:    v1alpha2.ConditionTypeInitialSync,
						Status:  metav1.ConditionTrue,
						Reason:  v1alpha2.ReasonInitialDeviceReadinessReached,
						Message: "All devices have been ready at least once",
					},
				)
			}

			condDevicesReady := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha2.ConditionTypeDevicesReady)

			if !allReady && condDevicesReady.Status != metav1.ConditionFalse {
				var msg string = "No devices found"
				if len(resource.Devices) > 0 {
					msg = fmt.Sprintf(
						"Device %d volume %d is %s",
						failedDevice.Minor, failedDevice.Volume, failedDevice.DiskState,
					)
				}
				meta.SetStatusCondition(
					&rvr.Status.Conditions,
					metav1.Condition{
						Type:    v1alpha2.ConditionTypeDevicesReady,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha2.ReasonDeviceIsNotReady,
						Message: msg,
					},
				)
			}

			if allReady && condDevicesReady.Status != metav1.ConditionTrue {
				var message string
				if condDevicesReady.Reason == v1alpha2.ReasonDeviceIsNotReady {
					prec := time.Second * 5
					message = fmt.Sprintf(
						"Recovered from %s to %s after <%v",
						v1alpha2.ReasonDeviceIsNotReady,
						v1alpha2.ReasonDeviceIsReady,
						time.Since(condDevicesReady.LastTransitionTime.Time).Truncate(prec)+prec,
					)
				} else {
					message = "All devices ready"
				}

				meta.SetStatusCondition(
					&rvr.Status.Conditions,
					metav1.Condition{
						Type:    v1alpha2.ConditionTypeDevicesReady,
						Status:  metav1.ConditionTrue,
						Reason:  v1alpha2.ReasonDeviceIsReady,
						Message: message,
					},
				)
			}

			// Role handling
			isPrimary := resource.Role == "Primary"
			meta.SetStatusCondition(
				&rvr.Status.Conditions,
				metav1.Condition{
					Type: v1alpha2.ConditionTypeIsPrimary,
					Status: If(
						isPrimary,
						metav1.ConditionTrue,
						metav1.ConditionFalse,
					),
					Reason: If(
						isPrimary,
						v1alpha2.ReasonResourceRoleIsPrimary,
						v1alpha2.ReasonResourceRoleIsNotPrimary,
					),
					Message: fmt.Sprintf("Resource is in a '%s' role", resource.Role),
				},
			)

			// Quorum
			noQuorumDevice, foundNoQuorum := uiter.Find(
				devicesIter,
				func(d *drbdsetup.Device) bool { return !d.Quorum },
			)

			quorumCond := metav1.Condition{
				Type: v1alpha2.ConditionTypeQuorum,
			}
			if foundNoQuorum {
				quorumCond.Status = metav1.ConditionFalse
				quorumCond.Reason = v1alpha2.ReasonNoQuorumStatus
				quorumCond.Message = fmt.Sprintf("Device %d not in quorum", noQuorumDevice.Minor)
			} else {
				quorumCond.Status = metav1.ConditionTrue
				quorumCond.Reason = v1alpha2.ReasonQuorumStatus
				quorumCond.Message = "All devices are in quorum"
			}
			meta.SetStatusCondition(&rvr.Status.Conditions, quorumCond)

			// SuspendedIO
			suspendedCond := metav1.Condition{
				Type: v1alpha2.ConditionTypeDiskIOSuspended,
			}
			switch {
			case resource.SuspendedFencing:
				suspendedCond.Status = metav1.ConditionTrue
				suspendedCond.Reason = v1alpha2.ReasonDiskIOSuspendedFencing
			case resource.SuspendedNoData:
				suspendedCond.Status = metav1.ConditionTrue
				suspendedCond.Reason = v1alpha2.ReasonDiskIOSuspendedNoData
			case resource.SuspendedQuorum:
				suspendedCond.Status = metav1.ConditionTrue
				suspendedCond.Reason = v1alpha2.ReasonDiskIOSuspendedQuorum
			case resource.SuspendedUser:
				suspendedCond.Status = metav1.ConditionTrue
				suspendedCond.Reason = v1alpha2.ReasonDiskIOSuspendedByUser
			case resource.Suspended:
				suspendedCond.Status = metav1.ConditionTrue
				suspendedCond.Reason = v1alpha2.ReasonDiskIOSuspendedUnknownReason
			default:
				suspendedCond.Status = metav1.ConditionFalse
				suspendedCond.Reason = v1alpha2.ReasonDiskIONotSuspendedStatus
			}
			meta.SetStatusCondition(&rvr.Status.Conditions, suspendedCond)

			// Ready handling
			rvr.RecalculateStatusConditionReady()

			return nil
		},
	)
}

func copyStatusFields(
	target *v1alpha2.DRBDStatus,
	source *drbdsetup.Resource,
) {
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
	target.Devices = make([]v1alpha2.DeviceStatus, 0, len(source.Devices))
	for _, d := range source.Devices {
		target.Devices = append(target.Devices, v1alpha2.DeviceStatus{
			Volume:       d.Volume,
			Minor:        d.Minor,
			DiskState:    d.DiskState,
			Client:       d.Client,
			Open:         d.Open,
			Quorum:       d.Quorum,
			Size:         d.Size,
			Read:         d.Read,
			Written:      d.Written,
			ALWrites:     d.ALWrites,
			BMWrites:     d.BMWrites,
			UpperPending: d.UpperPending,
			LowerPending: d.LowerPending,
		})
	}

	// Connections
	target.Connections = make([]v1alpha2.ConnectionStatus, 0, len(source.Connections))
	for _, c := range source.Connections {
		conn := v1alpha2.ConnectionStatus{
			PeerNodeId:      c.PeerNodeID,
			Name:            c.Name,
			ConnectionState: c.ConnectionState,
			Congested:       c.Congested,
			Peerrole:        c.Peerrole,
			TLS:             c.TLS,
			APInFlight:      c.APInFlight,
			RSInFlight:      c.RSInFlight,
		}

		// Paths
		conn.Paths = make([]v1alpha2.PathStatus, 0, len(c.Paths))
		for _, p := range c.Paths {
			conn.Paths = append(conn.Paths, v1alpha2.PathStatus{
				ThisHost: v1alpha2.HostStatus{
					Address: p.ThisHost.Address,
					Port:    p.ThisHost.Port,
					Family:  p.ThisHost.Family,
				},
				RemoteHost: v1alpha2.HostStatus{
					Address: p.RemoteHost.Address,
					Port:    p.RemoteHost.Port,
					Family:  p.RemoteHost.Family,
				},
				Established: p.Established,
			})
		}

		// Peer devices
		conn.PeerDevices = make([]v1alpha2.PeerDeviceStatus, 0, len(c.PeerDevices))
		for _, pd := range c.PeerDevices {
			conn.PeerDevices = append(conn.PeerDevices, v1alpha2.PeerDeviceStatus{
				Volume:                 pd.Volume,
				ReplicationState:       pd.ReplicationState,
				PeerDiskState:          pd.PeerDiskState,
				PeerClient:             pd.PeerClient,
				ResyncSuspended:        pd.ResyncSuspended,
				OutOfSync:              pd.OutOfSync,
				Pending:                pd.Pending,
				Unacked:                pd.Unacked,
				HasSyncDetails:         pd.HasSyncDetails,
				HasOnlineVerifyDetails: pd.HasOnlineVerifyDetails,
				PercentInSync:          fmt.Sprintf("%.2f", pd.PercentInSync),
			})
		}

		target.Connections = append(target.Connections, conn)
	}
}
