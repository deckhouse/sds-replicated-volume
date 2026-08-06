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

package framework

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// configurationRolloutSyncPoll is how often ObserveConfigurationRollout re-checks the
// initial-replay fence of its handler registration.
const configurationRolloutSyncPoll = 20 * time.Millisecond

// ConfigurationRolloutObserverOptions configures ObserveConfigurationRollout.
type ConfigurationRolloutObserverOptions struct {
	// RSCName is the storage class whose volumes are accounted. Required: the budget
	// is a per-class property, and a rollout of another class must not count.
	RSCName string

	// Names restricts the accounting to exactly these volume names. Empty means every
	// volume of the class, including ones created while the observer runs.
	Names []string

	// MaxParallel is the budget the class is expected to hold, i.e.
	// spec.configurationRolloutStrategy.rollingUpdate.maxParallel. Exceeding it is a
	// failure, not a datum: see Await. Required, must be positive.
	MaxParallel int

	// Active reports whether a volume is mid-rollout on the snapshot it is given: it
	// carries the new configuration and has not yet reached the layout that
	// configuration asks for. Required.
	Active func(*v1alpha1.ReplicatedVolume) bool

	// Waiting reports whether a volume is waiting for a rollout slot on the snapshot it
	// is given. Required.
	Waiting func(*v1alpha1.ReplicatedVolume) bool
}

// validate reports why the options cannot drive an observer.
func (o ConfigurationRolloutObserverOptions) validate() error {
	switch {
	case o.RSCName == "":
		return errors.New("RSCName must be set")
	case o.MaxParallel <= 0:
		return fmt.Errorf("MaxParallel must be positive, got %d", o.MaxParallel)
	case o.Active == nil:
		return errors.New("Active must be set")
	case o.Waiting == nil:
		return errors.New("Waiting must be set")
	default:
		return nil
	}
}

// ConfigurationRolloutSnapshot is a point-in-time reading of a
// ConfigurationRolloutObserver. All name lists are sorted.
type ConfigurationRolloutSnapshot struct {
	// Active are the volumes classified as mid-rollout by the latest event of each.
	Active []string
	// Waiting are the volumes classified as waiting for a slot by the latest event of each.
	Waiting []string
	// EverWaiting are the volumes that were observed waiting at least once. A volume that
	// waited and then rolled out stays in this list — the whole point of the wait is that
	// it happened, not that it still does.
	EverWaiting []string
	// MaxActive is the highest number of simultaneously active volumes ever observed.
	MaxActive int
	// OverLimit is set once MaxActive exceeded the budget, and never cleared.
	OverLimit bool
}

// String renders the snapshot for failure messages.
func (s ConfigurationRolloutSnapshot) String() string {
	return fmt.Sprintf("active=%v waiting=%v everWaiting=%v maxActive=%d overLimit=%v",
		s.Active, s.Waiting, s.EverWaiting, s.MaxActive, s.OverLimit)
}

// ConfigurationRolloutObserver measures how many volumes of one storage class carry a
// configuration they have not converged on yet, i.e. how much of the RollingUpdate
// budget the controller is actually using.
//
// It is built on ONE event handler registered on the suite's shared ReplicatedVolume
// informer, and on nothing else. That is deliberate: the initial replay of an event
// handler and the events that follow it arrive through the same ordered stream, so the
// accounting can never be corrupted by a stale reading overwriting a newer one — which
// is exactly what a cache List used as a seed alongside the stream would risk.
//
// ObserveConfigurationRollout returns only after the registration reports HasSynced, so
// the state of every volume that already existed is accounted before the caller makes
// its next move (typically the storage class edit that starts the rollout).
type ConfigurationRolloutObserver struct {
	mu     sync.Mutex
	core   *rolloutObserverCore
	notify chan struct{}

	maxParallel int

	informer ctrlcache.Informer
	reg      toolscache.ResourceEventHandlerRegistration
	stopped  bool
}

// ObserveConfigurationRollout starts observing the configuration rollout of one storage
// class and returns the observer.
//
// The observer stops itself through a DeferCleanup registered here; Stop may also be
// called explicitly and is idempotent.
func (f *Framework) ObserveConfigurationRollout(
	ctx context.Context,
	opts ConfigurationRolloutObserverOptions,
) *ConfigurationRolloutObserver {
	GinkgoHelper()
	if err := opts.validate(); err != nil {
		Fail(fmt.Sprintf("ObserveConfigurationRollout: %v", err))
	}

	o := &ConfigurationRolloutObserver{
		core:        newRolloutObserverCore(opts),
		notify:      make(chan struct{}),
		maxParallel: opts.MaxParallel,
	}

	informer, err := f.Cache.GetInformerForKind(ctx, gvkRV)
	if err != nil {
		Fail(fmt.Sprintf("ObserveConfigurationRollout: getting the %s informer: %v", gvkRV.Kind, err))
	}
	reg, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { o.handle(watch.Added, obj) },
		UpdateFunc: func(_, newObj any) { o.handle(watch.Modified, newObj) },
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			o.handle(watch.Deleted, obj)
		},
	})
	if err != nil {
		Fail(fmt.Sprintf("ObserveConfigurationRollout: adding the %s event handler: %v", gvkRV.Kind, err))
	}

	o.informer, o.reg = informer, reg
	DeferCleanup(o.Stop)

	if err := awaitHandlerSynced(ctx, reg.HasSynced, configurationRolloutSyncPoll); err != nil {
		Fail(fmt.Sprintf("ObserveConfigurationRollout: %v", err))
	}
	return o
}

// Snapshot returns the current reading of the accounting.
func (o *ConfigurationRolloutObserver) Snapshot() ConfigurationRolloutSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.core.snapshot()
}

// Await blocks until cond holds for a snapshot of the accounting, and returns that
// snapshot. description names what is being waited for; it is what the timeout message
// says the class failed to reach.
//
// Await fails the spec the moment the observed parallelism exceeds the budget. No wait
// this suite performs can legitimately be satisfied by an over-limit rollout, and
// failing at the observation rather than at a final assertion points at the event that
// broke the budget instead of at its aftermath.
func (o *ConfigurationRolloutObserver) Await(
	ctx context.Context,
	description string,
	cond func(ConfigurationRolloutSnapshot) bool,
) ConfigurationRolloutSnapshot {
	GinkgoHelper()
	for {
		o.mu.Lock()
		snapshot := o.core.snapshot()
		changed := o.notify
		o.mu.Unlock()

		if snapshot.OverLimit {
			Fail(fmt.Sprintf("configuration rollout exceeded maxParallel=%d while waiting for %s: %s",
				o.maxParallel, description, snapshot))
		}
		if cond(snapshot) {
			return snapshot
		}

		select {
		case <-changed:
		case <-ctx.Done():
			Fail(fmt.Sprintf("timed out waiting for %s: %s", description, snapshot))
			return snapshot
		}
	}
}

// AwaitMaxActive blocks until at least n volumes of the class have been observed
// rolling out at the same time.
func (o *ConfigurationRolloutObserver) AwaitMaxActive(ctx context.Context, n int) ConfigurationRolloutSnapshot {
	GinkgoHelper()
	return o.Await(ctx, fmt.Sprintf("%d volumes rolling out at the same time", n),
		func(s ConfigurationRolloutSnapshot) bool { return s.MaxActive >= n })
}

// AwaitWaiting blocks until at least n distinct volumes of the class have been observed
// waiting for a rollout slot.
func (o *ConfigurationRolloutObserver) AwaitWaiting(ctx context.Context, n int) ConfigurationRolloutSnapshot {
	GinkgoHelper()
	return o.Await(ctx, fmt.Sprintf("%d volumes waiting for a rollout slot", n),
		func(s ConfigurationRolloutSnapshot) bool { return len(s.EverWaiting) >= n })
}

// Stop unregisters the event handler. It is idempotent, and the accounting stays
// readable afterwards.
func (o *ConfigurationRolloutObserver) Stop() {
	o.mu.Lock()
	if o.stopped || o.informer == nil {
		o.mu.Unlock()
		return
	}
	o.stopped = true
	informer, reg := o.informer, o.reg
	o.mu.Unlock()

	// Outside the lock: the handler this removes takes the same lock, and a
	// client-go release that waits for it to drain would otherwise deadlock.
	_ = informer.RemoveEventHandler(reg)
}

// handle folds one informer event into the accounting and wakes the waiters, but only
// for an event that the cohort filter accepted: every volume of the suite reaches this
// handler, and waking on all of them would turn every unrelated status write into a
// re-evaluation.
func (o *ConfigurationRolloutObserver) handle(eventType watch.EventType, obj any) {
	rv, ok := obj.(*v1alpha1.ReplicatedVolume)
	if !ok {
		return
	}

	o.mu.Lock()
	accounted := o.core.observe(eventType, rv)
	var changed chan struct{}
	if accounted {
		changed, o.notify = o.notify, make(chan struct{})
	}
	o.mu.Unlock()

	if changed != nil {
		close(changed)
	}
}

// awaitHandlerSynced blocks until synced reports true, i.e. until the handler has been
// called for every object the informer held when it was registered.
//
// The poll interval is a parameter so unit tests drive it without waiting in real time.
func awaitHandlerSynced(ctx context.Context, synced func() bool, poll time.Duration) error {
	for {
		if synced() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for the event handler to replay the informer cache: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// rolloutObserverCore is the accounting a ConfigurationRolloutObserver folds events
// into. It owns the cohort filter as well, so that "which volumes count" and "how many
// of them are rolling out" are decided in one unit-testable place.
type rolloutObserverCore struct {
	rscName     string
	names       map[string]struct{}
	maxParallel int
	isActive    func(*v1alpha1.ReplicatedVolume) bool
	isWaiting   func(*v1alpha1.ReplicatedVolume) bool

	active      map[string]struct{}
	waiting     map[string]struct{}
	everWaiting map[string]struct{}
	maxActive   int
	overLimit   bool
}

// newRolloutObserverCore builds the accounting for validated options.
func newRolloutObserverCore(opts ConfigurationRolloutObserverOptions) *rolloutObserverCore {
	names := make(map[string]struct{}, len(opts.Names))
	for _, name := range opts.Names {
		names[name] = struct{}{}
	}
	return &rolloutObserverCore{
		rscName:     opts.RSCName,
		names:       names,
		maxParallel: opts.MaxParallel,
		isActive:    opts.Active,
		isWaiting:   opts.Waiting,
		active:      map[string]struct{}{},
		waiting:     map[string]struct{}{},
		everWaiting: map[string]struct{}{},
	}
}

// selects reports whether rv belongs to the observed cohort: the class always, and the
// explicit name list when one was given.
func (c *rolloutObserverCore) selects(rv *v1alpha1.ReplicatedVolume) bool {
	if rv.Spec.ReplicatedStorageClassName != c.rscName {
		return false
	}
	if len(c.names) == 0 {
		return true
	}
	_, ok := c.names[rv.Name]
	return ok
}

// observe folds one event into the accounting and reports whether it was accounted at
// all. A deleted volume leaves the current sets but keeps its mark on the history:
// deleting a volume mid-rollout must not erase the parallelism it was part of.
func (c *rolloutObserverCore) observe(eventType watch.EventType, rv *v1alpha1.ReplicatedVolume) bool {
	if !c.selects(rv) {
		return false
	}

	if eventType == watch.Deleted {
		delete(c.active, rv.Name)
		delete(c.waiting, rv.Name)
	} else {
		if c.isActive(rv) {
			c.active[rv.Name] = struct{}{}
		} else {
			delete(c.active, rv.Name)
		}
		if c.isWaiting(rv) {
			c.waiting[rv.Name] = struct{}{}
			c.everWaiting[rv.Name] = struct{}{}
		} else {
			delete(c.waiting, rv.Name)
		}
	}

	if len(c.active) > c.maxActive {
		c.maxActive = len(c.active)
	}
	if len(c.active) > c.maxParallel {
		c.overLimit = true
	}
	return true
}

// snapshot renders the accounting.
func (c *rolloutObserverCore) snapshot() ConfigurationRolloutSnapshot {
	return ConfigurationRolloutSnapshot{
		Active:      sortedKeys(c.active),
		Waiting:     sortedKeys(c.waiting),
		EverWaiting: sortedKeys(c.everWaiting),
		MaxActive:   c.maxActive,
		OverLimit:   c.overLimit,
	}
}

// sortedKeys returns the keys of a name set in a deterministic order.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
