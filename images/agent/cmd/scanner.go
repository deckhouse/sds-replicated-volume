package main

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"time"

	//lint:ignore ST1001 utils is the only exception
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/cooldown"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type updatedResourceName string

func runDRBDSetupScanner(
	ctx context.Context,
	log *slog.Logger,
	cl client.Client,
) (err error) {
	log = log.With("goroutine", "scanner")

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	batcher := cooldown.NewBatcher(appendUpdatedResourceNameToBatch)

	//
	go func() {
		cd := cooldown.NewExponentialCooldown(50*time.Millisecond, time.Second)
		for range batcher.ConsumeWithCooldown(ctx, cd) {

		}
	}()

	events2Cmd := drbdsetup.NewEvents2(ctx)

	var events2CmdErr error
	for ev := range processEvents(events2Cmd.Run(&events2CmdErr), false, log) {
		batcher.Add(ev)
	}

	if events2CmdErr != nil {
		return LogError(log, fmt.Errorf("run events2: %w", events2CmdErr))
	}

	return
}

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

func processEvents(
	allEvents iter.Seq[drbdsetup.Events2Result],
	online bool,
	log *slog.Logger,
) iter.Seq[updatedResourceName] {
	return func(yield func(updatedResourceName) bool) {
		log = log.With("goroutine", "scanner/filterEvents")
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

			log.Debug("parsed event", "event", typedEvent)

			if !online {
				if typedEvent.Kind == "exists" && typedEvent.Object == "-" {
					online = true
					log.Debug("events online")
				}
				continue
			}

			if resourceName, ok := typedEvent.State["name"]; !ok {

			} else {

			}

			if !yield(typedEvent) {
				return
			}
		}
	}
}

func updateReplicaStatusIfNeeded(
	ctx context.Context,
	cl client.Client,
	log *slog.Logger,
) error {
	return nil
}
