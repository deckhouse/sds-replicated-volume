package utils

import (
	"context"
	"sync"
	"time"
)

type ExponentialCooldown struct {
	initialDelay time.Duration
	maxDelay     time.Duration
	mu           *sync.Mutex

	// mutable:

	lastHit      time.Time
	currentDelay time.Duration
}

func NewExponentialCooldown(
	initialDelay time.Duration,
	maxDelay time.Duration,
) *ExponentialCooldown {
	if initialDelay < time.Nanosecond {
		panic("expected initialDelay to be positive")
	}
	if maxDelay < initialDelay {
		panic("expected maxDelay to be greater or equal to initialDelay")
	}

	return &ExponentialCooldown{
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
		mu:           &sync.Mutex{},

		currentDelay: initialDelay,
	}
}

func (cd *ExponentialCooldown) Hit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	// repeating cancelation check, since lock may have taken a long time
	if err := ctx.Err(); err != nil {
		return err
	}

	sinceLastHit := time.Since(cd.lastHit)

	if sinceLastHit >= cd.currentDelay {
		// cooldown has passed by itself - resetting the delay
		cd.lastHit = time.Now()
		cd.currentDelay = cd.initialDelay
		return nil
	}

	// inside a cooldown
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(cd.currentDelay - sinceLastHit):
		// cooldown has passed just now - doubling the delay
		cd.lastHit = time.Now()
		cd.currentDelay = min(cd.currentDelay*2, cd.maxDelay)
		return nil
	}
}
