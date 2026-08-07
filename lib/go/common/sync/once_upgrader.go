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

package sync

import (
	"context"
	"fmt"
)

// OnceUpgrader lets no caller through until an upgrade has succeeded. Callers
// block while an attempt is in flight, and each makes at most one attempt of its
// own, so retrying is the caller's decision.
type OnceUpgrader struct {
	// ticket represents an upgrade attempt to be accomplished;
	// always no more then 1 ticket in the channel
	tickets   chan struct{}
	upgradeFn func(context.Context) error
}

// NewOnceUpgrader with enabled false returns an upgrader that lets every caller
// straight through, forever.
func NewOnceUpgrader(enabled bool, upgradeFn func(context.Context) error) *OnceUpgrader {
	res := &OnceUpgrader{
		tickets:   make(chan struct{}, 1),
		upgradeFn: upgradeFn,
	}
	if enabled {
		// schedule an upgrade attempt
		res.tickets <- struct{}{}
	} else {
		// just like an upgrade has already been successful
		close(res.tickets)
	}
	return res
}

// EnsureUpgraded returns nil once some caller's attempt has succeeded, the upgrade
// function's error when this caller is the one that ran it, or ctx's error when ctx
// ends first.
func (u *OnceUpgrader) EnsureUpgraded(ctx context.Context) error {
	select {
	case _, ok := <-u.tickets:
		if !ok {
			// channel closed: post-success fast-path
			return nil
		}

		var upgradeErr error
		if upgradeErr = u.upgradeFn(ctx); upgradeErr != nil {
			// allow another attempt
			u.tickets <- struct{}{}
		} else {
			// success - no more attempts
			close(u.tickets)
		}
		// always only one attempt per caller
		return upgradeErr
	case <-ctx.Done():
		return fmt.Errorf("waiting for upgrade: %w", ctx.Err())
	}
}
