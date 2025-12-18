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
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/cooldown"
	u "github.com/deckhouse/sds-common-lib/utils"
	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

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
			Steps:    7,
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
	if !slices.ContainsFunc(
		batch,
		func(e updatedResourceName) bool { return e == newItem },
	) {
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
				return u.LogError(log, fmt.Errorf("getting statusResult: %w", err))
			}

			log.Debug("got status for 'n' resources", "n", len(statusResult))

			rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}

			// we expect this query to hit cache with index
			err = s.cl.List(
				s.ctx,
				rvrList,
				client.MatchingFieldsSelector{
					Selector: (&v1alpha3.ReplicatedVolumeReplica{}).
						NodeNameSelector(s.hostname),
				},
			)
			if err != nil {
				return u.LogError(log, fmt.Errorf("listing rvr: %w", err))
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
					func(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
						// TODO
						return rvr.Spec.ReplicatedVolumeName == resourceName
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
					return u.LogError(
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

func (s *Scanner) updateReplicaStatusIfNeeded(
	rvr *v1alpha3.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) error {
	statusPatch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha3.DRBD{}
	}
	if rvr.Status.DRBD.Status == nil {
		rvr.Status.DRBD.Status = &v1alpha3.DRBDStatus{}
	}
	copyStatusFields(rvr.Status.DRBD.Status, resource)

	s.updateReplicaStatusConditionDataInitialized(rvr, resource)
	s.updateReplicaStatusConditionInQuorum(rvr, resource)

	if err := s.cl.Status().Patch(s.ctx, rvr, statusPatch); err != nil {
		return fmt.Errorf("patching status: %w", err)
	}

	return nil
}

func (s *Scanner) updateReplicaStatusConditionDataInitialized(
	rvr *v1alpha3.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) {
	diskful := rvr.Spec.Type == "Diskful"

	if !diskful {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:               v1alpha3.ConditionTypeDataInitialized,
				Status:             v1.ConditionFalse,
				Reason:             v1alpha3.ReasonNotApplicableToDiskless,
				ObservedGeneration: rvr.Generation,
			},
		)
		return
	}

	alreadyTrue := meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha3.ConditionTypeDataInitialized)
	if alreadyTrue {
		return
	}

	becameTrue := len(resource.Devices) > 0 && resource.Devices[0].DiskState == "UpToDate"
	if becameTrue {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			v1.Condition{
				Type:               v1alpha3.ConditionTypeDataInitialized,
				Status:             v1.ConditionTrue,
				Reason:             v1alpha3.ReasonDiskHasBeenSeenInUpToDateState,
				ObservedGeneration: rvr.Generation,
			},
		)
		return
	}

	alreadyFalse := meta.IsStatusConditionFalse(rvr.Status.Conditions, v1alpha3.ConditionTypeDataInitialized)
	if alreadyFalse {
		return
	}

	meta.SetStatusCondition(
		&rvr.Status.Conditions,
		v1.Condition{
			Type:               v1alpha3.ConditionTypeDataInitialized,
			Status:             v1.ConditionFalse,
			Reason:             v1alpha3.ReasonDiskNeverWasInUpToDateState,
			ObservedGeneration: rvr.Generation,
		},
	)
}

func (s *Scanner) updateReplicaStatusConditionInQuorum(
	rvr *v1alpha3.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) {
	if len(resource.Devices) == 0 {
		s.log.Warn("no devices reported by DRBD")
		return
	}

	newCond := v1.Condition{Type: v1alpha3.ConditionTypeInQuorum}
	var condUpdated bool

	inQuorum := resource.Devices[0].Quorum

	oldCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeInQuorum)
	if oldCond == nil || oldCond.Status == v1.ConditionUnknown {
		// initial setup - simpler message
		if inQuorum {
			newCond.Status, newCond.Reason = v1.ConditionTrue, v1alpha3.ReasonInQuorumInQuorum
		} else {
			newCond.Status, newCond.Reason = v1.ConditionFalse, v1alpha3.ReasonInQuorumQuorumLost
		}
		condUpdated = true
	} else {
		if inQuorum && oldCond.Status != v1.ConditionTrue {
			// switch to true
			newCond.Status, newCond.Reason = v1.ConditionTrue, v1alpha3.ReasonInQuorumInQuorum
			newCond.Message = fmt.Sprintf("Quorum achieved after being lost for %v", time.Since(oldCond.LastTransitionTime.Time))
			condUpdated = true
		} else if !inQuorum && oldCond.Status != v1.ConditionFalse {
			// switch to false
			newCond.Status, newCond.Reason = v1.ConditionFalse, v1alpha3.ReasonInQuorumQuorumLost
			newCond.Message = fmt.Sprintf("Quorum lost after being achieved for %v", time.Since(oldCond.LastTransitionTime.Time))
			condUpdated = true
		}
	}

	if condUpdated {
		meta.SetStatusCondition(&rvr.Status.Conditions, newCond)
	}
}

func copyStatusFields(
	target *v1alpha3.DRBDStatus,
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
	target.Devices = make([]v1alpha3.DeviceStatus, 0, len(source.Devices))
	for _, d := range source.Devices {
		target.Devices = append(target.Devices, v1alpha3.DeviceStatus{
			Volume:       d.Volume,
			Minor:        d.Minor,
			DiskState:    v1alpha3.ParseDiskState(d.DiskState),
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
	target.Connections = make([]v1alpha3.ConnectionStatus, 0, len(source.Connections))
	for _, c := range source.Connections {
		conn := v1alpha3.ConnectionStatus{
			PeerNodeId:      c.PeerNodeID,
			Name:            c.Name,
			ConnectionState: v1alpha3.ParseConnectionState(c.ConnectionState),
			Congested:       c.Congested,
			Peerrole:        c.Peerrole,
			TLS:             c.TLS,
			APInFlight:      c.APInFlight,
			RSInFlight:      c.RSInFlight,
		}

		// Paths
		conn.Paths = make([]v1alpha3.PathStatus, 0, len(c.Paths))
		for _, p := range c.Paths {
			conn.Paths = append(conn.Paths, v1alpha3.PathStatus{
				ThisHost: v1alpha3.HostStatus{
					Address: p.ThisHost.Address,
					Port:    p.ThisHost.Port,
					Family:  p.ThisHost.Family,
				},
				RemoteHost: v1alpha3.HostStatus{
					Address: p.RemoteHost.Address,
					Port:    p.RemoteHost.Port,
					Family:  p.RemoteHost.Family,
				},
				Established: p.Established,
			})
		}

		// Peer devices
		conn.PeerDevices = make([]v1alpha3.PeerDeviceStatus, 0, len(c.PeerDevices))
		for _, pd := range c.PeerDevices {
			conn.PeerDevices = append(conn.PeerDevices, v1alpha3.PeerDeviceStatus{
				Volume:                 pd.Volume,
				ReplicationState:       v1alpha3.ParseReplicationState(pd.ReplicationState),
				PeerDiskState:          v1alpha3.ParseDiskState(pd.PeerDiskState),
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
