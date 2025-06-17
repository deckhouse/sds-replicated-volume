package main

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/jinzhu/copier"

	"github.com/deckhouse/sds-common-lib/cooldown"
	//lint:ignore ST1001 utils is the only exception
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scanner struct {
	log      *slog.Logger
	hostname string
	// current run context
	ctx context.Context
	// cancels current run context
	cancel context.CancelCauseFunc
	// 1) react to:
	events2 *drbdsetup.Events2
	// 2) put events into:
	batcher *cooldown.Batcher
	// 3) get full status from:
	status *drbdsetup.Status
	// 4) update k8s resources with:
	cl client.Client
}

func NewScanner(
	ctx context.Context,
	log *slog.Logger,
	cl client.Client,
	hostname string,
) *scanner {
	ctx, cancel := context.WithCancelCause(ctx)
	s := &scanner{
		hostname: hostname,
		ctx:      ctx,
		cancel:   cancel,
		log:      log.With("goroutine", "scanner"),
		cl:       cl,
		batcher:  cooldown.NewBatcher(appendUpdatedResourceNameToBatch),
		events2:  drbdsetup.NewEvents2(ctx),
		status:   drbdsetup.NewStatus(ctx),
	}
	return s
}

func (s *scanner) Run() error {
	// consume from batch
	go func() {
		var err error
		defer func() { s.cancel(fmt.Errorf("batch consumer: %w", err)) }()
		defer RecoverPanicToErr(&err)
		err = s.consumeBatches()
	}()

	var events2CmdErr error

	for ev := range s.processEvents(s.events2.Run(&events2CmdErr), false) {
		s.batcher.Add(ev)
	}

	if events2CmdErr != nil {
		return LogError(s.log, fmt.Errorf("run events2: %w", events2CmdErr))
	}

	return nil
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
	online bool,
) iter.Seq[updatedResourceName] {
	return func(yield func(updatedResourceName) bool) {
		log := s.log.With("goroutine", "scanner/processEvents")
		for ev := range allEvents {
			var typedEvent *drbdsetup.Event

			switch tev := ev.(type) {
			case *drbdsetup.Event:
				typedEvent = tev
			case *drbdsetup.UnparsedEvent:
				log.Warn(
					"unparsed event",
					"err", tev.Err,
					"line", tev.RawEventLine,
				)
				continue
			default:
				log.Error(
					"unexpected event type",
					"event", fmt.Sprintf("%v", tev),
				)
				continue
			}

			if !online {
				if typedEvent.Kind == "exists" && typedEvent.Object == "-" {
					online = true
					log.Debug("events online")
				}
				continue
			}

			if resourceName, ok := typedEvent.State["name"]; !ok {
				log.Debug("skipping event without name")
				continue
			} else {
				log.Debug("yielding event", "event", typedEvent)
				if !yield(updatedResourceName(resourceName)) {
					return
				}
			}
		}
	}
}

func (s *scanner) consumeBatches() error {
	cd := cooldown.NewExponentialCooldown(
		50*time.Millisecond,
		5*time.Second,
	)
	log := s.log.With("goroutine", "scanner/consumeBatches")

	for batch := range s.batcher.ConsumeWithCooldown(s.ctx, cd) {
		log.Debug("got batch of 'n' resources", "n", len(batch))

		statusResult, err := s.status.Run()
		if err != nil {
			return fmt.Errorf("getting statusResult: %w", err)
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
			return fmt.Errorf("listing rvr: %w", err)
		}

		for _, item := range batch {
			resourceName := string(item.(updatedResourceName))

			resourceStatus := SliceFind(
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

			rvr := SliceFind(
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
				return fmt.Errorf("updating replica status: %w", err)
			}
		}
	}

	return nil
}

func (s *scanner) updateReplicaStatusIfNeeded(
	rvr *v1alpha2.ReplicatedVolumeReplica,
	resource *drbdsetup.Resource,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status == nil {
		rvr.Status = &v1alpha2.ReplicatedVolumeReplicaStatus{}
	}

	if err := copier.Copy(&rvr.Status.DRBD, resource); err != nil {
		return fmt.Errorf("failed to copy status fields: %w", err)
	}

	return s.cl.Status().Patch(s.ctx, rvr, patch)
}
