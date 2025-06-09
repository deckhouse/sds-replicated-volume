package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	//lint:ignore ST1001 utils is the only exception
	. "github.com/deckhouse/sds-replicated-volume/images/agent/internal/utils"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

func runDRBDSetupScanner(
	ctx context.Context,
	log *slog.Logger,
	cl client.Client,
) (err error) {
	log = log.With("goroutine", "scanner")

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	eventsCh := make(chan drbdsetup.Events2Result)

	// go func() {
	// 	var err error
	// 	err = runEventsDispatcher
	// }()

	events2Cmd := drbdsetup.NewEvents2(ctx)

	if err := events2Cmd.Run(eventsCh); err != nil {
		return LogError(log, fmt.Errorf("run events2 command: %w", err))
	}

	return
}

func runEventsDispatcher(
	log *slog.Logger,
	srcEventsCh chan drbdsetup.Events2Result,
) error {
	log = log.With("goroutine", "scanner/eventsDispatcher")

	var online bool

	for ev := range srcEventsCh {
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

		//

	}

	return nil
}

type DRBDStatusUpdater struct {
	mu              *sync.Mutex
	cond            *sync.Cond
	updateTriggered bool
}

func NewDRBDStatusUpdater() *DRBDStatusUpdater {
	mu := &sync.Mutex{}
	return &DRBDStatusUpdater{
		cond: sync.NewCond(mu),
	}
}

func (u *DRBDStatusUpdater) TriggerUpdate() {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.updateTriggered = true

	u.cond.Signal()
}

func (u *DRBDStatusUpdater) Run(ctx context.Context) error {

	// TODO awake on context cancel

	cooldown := NewExponentialCooldown(100*time.Millisecond, 5*time.Second)

	for {
		if err := u.waitForTriggerIfNotAlready(ctx); err != nil {
			return err // context cancelation
		}

		if err := cooldown.Hit(ctx); err != nil {
			return err // context cancelation
		}

		if err := u.updateStatusIfNeeded(ctx); err != nil {
			return fmt.Errorf("updating replica status: %w", err)
		}
	}
}

func (u *DRBDStatusUpdater) waitForTriggerIfNotAlready(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	defer func() {
		u.updateTriggered = false
	}()

	// it has already been triggered, while we were not waiting
	if u.updateTriggered {
		return nil
	}

	// awakener is a goroutine, which will call "fake" Signal in order to stop
	// Wait() on context cancelation
	awakenerDone := make(chan struct{})
	defer func() {
		<-awakenerDone
	}()

	awakenerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
		case <-awakenerCtx.Done():
		}
		u.cond.Signal()
		awakenerDone <- struct{}{}
	}()

	u.cond.Wait()

	return ctx.Err()
}

func (u *DRBDStatusUpdater) updateStatusIfNeeded(
	ctx context.Context,
) error {
	return nil
}
