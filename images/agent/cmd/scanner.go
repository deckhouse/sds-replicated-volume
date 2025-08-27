package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"time"

	"github.com/deckhouse/sds-common-lib/cooldown"
	. "github.com/deckhouse/sds-common-lib/utils"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	"github.com/jinzhu/copier"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scanner struct {
	log      *slog.Logger
	hostname string
	ctx      context.Context
	cancel   context.CancelCauseFunc
	batcher  *cooldown.Batcher
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

func (s *scanner) Run() error {
	var err error

	for ev := range s.processEvents(drbdsetup.ExecuteEvents2(s.ctx, &err)) {
		s.log.Debug("added resource update event", "resource", ev)
		s.batcher.Add(ev)
	}

	if err != nil {
		return LogError(s.log, fmt.Errorf("run events2: %w", err))
	}

	return s.ctx.Err()
}

type updatedResourceName string

func appendUpdatedResourceNameToBatch(batch []any, newItem any) []any {
	resName := newItem.(updatedResourceName)
	if !slices.ContainsFunc(
		batch,
		func(e any) bool { return e.(updatedResourceName) == resName },
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

			if !online {
				continue
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
	cd := cooldown.NewExponentialCooldown(
		50*time.Millisecond,
		5*time.Second,
	)
	log := s.log.With("goroutine", "scanner/consumeBatches")

	for batch := range s.batcher.ConsumeWithCooldown(s.ctx, cd) {
		log.Debug("got batch of 'n' resources", "n", len(batch))

		statusResult, err := drbdsetup.ExecuteStatus(s.ctx)
		if err != nil {
			return LogError(log, fmt.Errorf("getting statusResult: %w", err))
		}

		log.Debug("got status for 'n' resources", "n", len(statusResult))

		rvrList := &v1alpha2.ReplicatedVolumeReplicaList{}

		// we expect this query to hit cache
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
			resourceName := string(item.(updatedResourceName))

			resourceStatus := uslices.Find(
				statusResult,
				func(res *drbdsetup.Resource) bool { return res.Name == resourceName },
			)
			if resourceStatus == nil {
				log.Warn(
					"got update event for resource 'resourceName', but it's missing in drbdsetup status",
					"resourceName", resourceName,
				)
				continue
			}

			rvr := uslices.Find(
				rvrList.Items,
				func(rvr *v1alpha2.ReplicatedVolumeReplica) bool {
					return rvr.Spec.ReplicatedVolumeName == resourceName
				},
			)
			if rvr == nil {
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
}

func (s *scanner) updateReplicaStatusIfNeeded(
	rvr *v1alpha2.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status == nil {
		rvr.Status = &v1alpha2.ReplicatedVolumeReplicaStatus{}
		rvr.Status.Conditions = []metav1.Condition{}
	}

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha2.DRBDStatus{}
	}

	if err := copier.Copy(rvr.Status.DRBD, resource); err != nil {
		return fmt.Errorf("failed to copy status fields: %w", err)
	}

	allUpToDate := uslices.Find(resource.Devices, func(d *drbdsetup.Device) bool { return d.DiskState != "UpToDate" }) == nil
	if !meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha2.ConditionTypeInitialSync) && allUpToDate {
		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:    v1alpha2.ConditionTypeInitialSync,
				Status:  metav1.ConditionTrue,
				Reason:  v1alpha2.ReasonInitialUpToDateReached,
				Message: "All device disk states were UpToDate at least once",
			},
		)
	}

	return s.cl.Status().Patch(s.ctx, rvr, patch)
}
