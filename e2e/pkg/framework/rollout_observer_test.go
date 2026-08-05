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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	testRolloutClass = "rsc-observed"
	testRolloutPhase = "e2e.test/rollout-phase"

	testRolloutIdle    = "idle"
	testRolloutActive  = "active"
	testRolloutWaiting = "waiting"
)

// rolloutRV builds a ReplicatedVolume the observer core can classify. The phase is
// carried in an annotation so that these tests exercise the accounting, not the domain
// projection the specs plug in.
func rolloutRV(name, rscName, phase string) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{testRolloutPhase: phase},
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: rscName},
	}
}

// rolloutPhaseIs builds a predicate over the annotation rolloutRV writes.
func rolloutPhaseIs(phase string) func(*v1alpha1.ReplicatedVolume) bool {
	return func(rv *v1alpha1.ReplicatedVolume) bool {
		return rv.Annotations[testRolloutPhase] == phase
	}
}

// testRolloutOptions are the options every accounting test starts from.
func testRolloutOptions(maxParallel int, names ...string) ConfigurationRolloutObserverOptions {
	return ConfigurationRolloutObserverOptions{
		RSCName:     testRolloutClass,
		Names:       names,
		MaxParallel: maxParallel,
		Active:      rolloutPhaseIs(testRolloutActive),
		Waiting:     rolloutPhaseIs(testRolloutWaiting),
	}
}

var _ = Describe("ConfigurationRolloutObserverOptions.validate", func() {
	It("accepts complete options", func() {
		Expect(testRolloutOptions(2).validate()).To(Succeed())
	})

	It("rejects options without a storage class", func() {
		opts := testRolloutOptions(2)
		opts.RSCName = ""
		Expect(opts.validate()).To(MatchError(ContainSubstring("RSCName")))
	})

	It("rejects a non-positive budget", func() {
		opts := testRolloutOptions(0)
		Expect(opts.validate()).To(MatchError(ContainSubstring("MaxParallel")))
	})

	It("rejects options without classifiers", func() {
		opts := testRolloutOptions(2)
		opts.Active = nil
		Expect(opts.validate()).To(MatchError(ContainSubstring("Active")))

		opts = testRolloutOptions(2)
		opts.Waiting = nil
		Expect(opts.validate()).To(MatchError(ContainSubstring("Waiting")))
	})
})

var _ = Describe("rolloutObserverCore", func() {
	It("accounts an ordered add/update/delete stream", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		Expect(core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutWaiting))).To(BeTrue())
		Expect(core.snapshot().Waiting).To(Equal([]string{"rv-a"}))
		Expect(core.snapshot().Active).To(BeEmpty())

		Expect(core.observe(watch.Modified, rolloutRV("rv-a", testRolloutClass, testRolloutActive))).To(BeTrue())
		snapshot := core.snapshot()
		Expect(snapshot.Active).To(Equal([]string{"rv-a"}))
		Expect(snapshot.Waiting).To(BeEmpty())
		Expect(snapshot.EverWaiting).To(Equal([]string{"rv-a"}))

		Expect(core.observe(watch.Modified, rolloutRV("rv-a", testRolloutClass, testRolloutIdle))).To(BeTrue())
		Expect(core.snapshot().Active).To(BeEmpty())

		Expect(core.observe(watch.Deleted, rolloutRV("rv-a", testRolloutClass, testRolloutActive))).To(BeTrue())
		snapshot = core.snapshot()
		Expect(snapshot.Active).To(BeEmpty())
		Expect(snapshot.Waiting).To(BeEmpty())
	})

	It("keeps a volume deleted mid-rollout out of the current sets but in the history", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutWaiting))
		core.observe(watch.Modified, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		core.observe(watch.Deleted, rolloutRV("rv-a", testRolloutClass, testRolloutActive))

		snapshot := core.snapshot()
		Expect(snapshot.Active).To(BeEmpty())
		Expect(snapshot.EverWaiting).To(Equal([]string{"rv-a"}))
		Expect(snapshot.MaxActive).To(Equal(1))
	})

	It("ignores volumes of another storage class", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		Expect(core.observe(watch.Added, rolloutRV("rv-a", "rsc-other", testRolloutActive))).To(BeFalse())
		Expect(core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass+"-suffix", testRolloutActive))).To(BeFalse())

		snapshot := core.snapshot()
		Expect(snapshot.Active).To(BeEmpty())
		Expect(snapshot.MaxActive).To(BeZero())
	})

	It("ignores volumes outside an explicit name list", func() {
		core := newRolloutObserverCore(testRolloutOptions(2, "rv-a", "rv-b"))

		Expect(core.observe(watch.Added, rolloutRV("rv-c", testRolloutClass, testRolloutActive))).To(BeFalse())
		Expect(core.observe(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutActive))).To(BeTrue())

		Expect(core.snapshot().Active).To(Equal([]string{"rv-b"}))
	})

	It("accounts every volume of the class when no name list is given", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		Expect(core.observe(watch.Added, rolloutRV("rv-late", testRolloutClass, testRolloutActive))).To(BeTrue())
		Expect(core.snapshot().Active).To(Equal([]string{"rv-late"}))
	})

	It("remembers the peak parallelism, not the current one", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		core.observe(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutActive))
		Expect(core.snapshot().MaxActive).To(Equal(2))

		core.observe(watch.Modified, rolloutRV("rv-a", testRolloutClass, testRolloutIdle))
		core.observe(watch.Modified, rolloutRV("rv-b", testRolloutClass, testRolloutIdle))

		snapshot := core.snapshot()
		Expect(snapshot.Active).To(BeEmpty())
		Expect(snapshot.MaxActive).To(Equal(2))
		Expect(snapshot.OverLimit).To(BeFalse())
	})

	It("collects every volume that ever waited, sorted", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		core.observe(watch.Added, rolloutRV("rv-c", testRolloutClass, testRolloutWaiting))
		core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutWaiting))
		core.observe(watch.Modified, rolloutRV("rv-c", testRolloutClass, testRolloutActive))

		snapshot := core.snapshot()
		Expect(snapshot.Waiting).To(Equal([]string{"rv-a"}))
		Expect(snapshot.EverWaiting).To(Equal([]string{"rv-a", "rv-c"}))
	})

	It("latches over-limit once the budget is exceeded", func() {
		core := newRolloutObserverCore(testRolloutOptions(2))

		core.observe(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		core.observe(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutActive))
		Expect(core.snapshot().OverLimit).To(BeFalse())

		core.observe(watch.Added, rolloutRV("rv-c", testRolloutClass, testRolloutActive))
		snapshot := core.snapshot()
		Expect(snapshot.OverLimit).To(BeTrue())
		Expect(snapshot.MaxActive).To(Equal(3))

		core.observe(watch.Deleted, rolloutRV("rv-c", testRolloutClass, testRolloutActive))
		core.observe(watch.Modified, rolloutRV("rv-b", testRolloutClass, testRolloutIdle))
		core.observe(watch.Modified, rolloutRV("rv-a", testRolloutClass, testRolloutIdle))

		snapshot = core.snapshot()
		Expect(snapshot.Active).To(BeEmpty())
		Expect(snapshot.OverLimit).To(BeTrue(), "an over-limit rollout stays over-limit after it drains")
	})
})

// stubRegistration is a ResourceEventHandlerRegistration whose replay fence the test
// controls.
type stubRegistration struct {
	mu     sync.Mutex
	synced bool
	calls  int
}

func (r *stubRegistration) HasSynced() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.synced
}

func (r *stubRegistration) sync() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synced = true
}

// stubInformer records the registrations removed from it.
type stubInformer struct {
	mu      sync.Mutex
	removed []toolscache.ResourceEventHandlerRegistration
	err     error
}

func (i *stubInformer) AddEventHandler(toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, errors.New("not used by these tests")
}

func (i *stubInformer) AddEventHandlerWithResyncPeriod(toolscache.ResourceEventHandler, time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, errors.New("not used by these tests")
}

func (i *stubInformer) AddEventHandlerWithOptions(toolscache.ResourceEventHandler, toolscache.HandlerOptions) (toolscache.ResourceEventHandlerRegistration, error) {
	return nil, errors.New("not used by these tests")
}

func (i *stubInformer) RemoveEventHandler(handle toolscache.ResourceEventHandlerRegistration) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.removed = append(i.removed, handle)
	return i.err
}

func (i *stubInformer) AddIndexers(toolscache.Indexers) error { return nil }
func (i *stubInformer) HasSynced() bool                       { return true }
func (i *stubInformer) IsStopped() bool                       { return false }

func (i *stubInformer) removedHandles() []toolscache.ResourceEventHandlerRegistration {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]toolscache.ResourceEventHandlerRegistration(nil), i.removed...)
}

// newTestObserver wires an observer onto stub plumbing, the way
// ObserveConfigurationRollout wires it onto the suite's shared informer.
func newTestObserver(informer *stubInformer, reg toolscache.ResourceEventHandlerRegistration,
	opts ConfigurationRolloutObserverOptions) *ConfigurationRolloutObserver {
	return &ConfigurationRolloutObserver{
		core:        newRolloutObserverCore(opts),
		notify:      make(chan struct{}),
		maxParallel: opts.MaxParallel,
		informer:    informer,
		reg:         reg,
	}
}

var _ = Describe("awaitHandlerSynced", func() {
	It("returns once the registration has replayed the cache", func(ctx SpecContext) {
		reg := &stubRegistration{}
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- awaitHandlerSynced(ctx, reg.HasSynced, time.Millisecond)
		}()

		Consistently(done, 50*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())
		reg.sync()
		Eventually(done).Should(Receive(BeNil()))
	})

	It("reports the context that ran out instead of blocking forever", func(ctx SpecContext) {
		bounded, cancel := context.WithCancel(ctx)
		cancel()
		err := awaitHandlerSynced(bounded, func() bool { return false }, time.Millisecond)
		Expect(err).To(MatchError(context.Canceled))
		Expect(err.Error()).To(ContainSubstring("replay the informer cache"))
	})
})

var _ = Describe("ConfigurationRolloutObserver", func() {
	It("folds informer events into the accounting", func() {
		o := newTestObserver(&stubInformer{}, &stubRegistration{}, testRolloutOptions(2))

		o.handle(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		o.handle(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutWaiting))
		o.handle(watch.Added, rolloutRV("rv-x", "rsc-other", testRolloutActive))

		snapshot := o.Snapshot()
		Expect(snapshot.Active).To(Equal([]string{"rv-a"}))
		Expect(snapshot.Waiting).To(Equal([]string{"rv-b"}))
		Expect(snapshot.MaxActive).To(Equal(1))
	})

	It("ignores an event carrying something that is not a volume", func() {
		o := newTestObserver(&stubInformer{}, &stubRegistration{}, testRolloutOptions(2))
		o.handle(watch.Added, "not an object")
		Expect(o.Snapshot().MaxActive).To(BeZero())
	})

	It("wakes a wait when an accounted event arrives", func(ctx SpecContext) {
		o := newTestObserver(&stubInformer{}, &stubRegistration{}, testRolloutOptions(2))
		done := make(chan ConfigurationRolloutSnapshot, 1)
		go func() {
			defer GinkgoRecover()
			done <- o.AwaitWaiting(ctx, 2)
		}()

		o.handle(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutWaiting))
		Consistently(done, 50*time.Millisecond, 10*time.Millisecond).ShouldNot(Receive())

		o.handle(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutWaiting))
		var snapshot ConfigurationRolloutSnapshot
		Eventually(done).Should(Receive(&snapshot))
		Expect(snapshot.EverWaiting).To(Equal([]string{"rv-a", "rv-b"}))
	})

	It("returns immediately when the wait is already satisfied", func(ctx SpecContext) {
		o := newTestObserver(&stubInformer{}, &stubRegistration{}, testRolloutOptions(2))
		o.handle(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		o.handle(watch.Added, rolloutRV("rv-b", testRolloutClass, testRolloutActive))

		Expect(o.AwaitMaxActive(ctx, 2).MaxActive).To(Equal(2))
	})

	It("unregisters once, however many times it is stopped", func() {
		informer := &stubInformer{}
		reg := &stubRegistration{}
		o := newTestObserver(informer, reg, testRolloutOptions(2))

		o.Stop()
		o.Stop()

		removed := informer.removedHandles()
		Expect(removed).To(HaveLen(1))
		Expect(removed[0]).To(BeIdenticalTo(reg))
	})

	It("keeps the accounting readable after it stops", func() {
		o := newTestObserver(&stubInformer{}, &stubRegistration{}, testRolloutOptions(2))
		o.handle(watch.Added, rolloutRV("rv-a", testRolloutClass, testRolloutActive))
		o.Stop()
		Expect(o.Snapshot().MaxActive).To(Equal(1))
	})
})
