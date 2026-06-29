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

package testkit

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

func makeRVR(name, phase string) *testObj {
	return makeTestObj(name, phase)
}

// ---------------------------------------------------------------------------
// Snapshot history
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject snapshot history", func() {
	It("starts with empty history", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		Expect(to.snapshotCount()).To(Equal(0))
	})

	It("injectEvent appends snapshot with correct ID", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Expect(to.snapshotCount()).To(Equal(2))

		to.mu.RLock()
		defer to.mu.RUnlock()
		Expect(to.snapshots[0].ID).To(Equal(0))
		Expect(to.snapshots[1].ID).To(Equal(1))
	})

	It("snapshots have increasing timestamps", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		time.Sleep(time.Millisecond)
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))

		to.mu.RLock()
		defer to.mu.RUnlock()
		Expect(to.snapshots[1].Timestamp).To(BeTemporally(">=", to.snapshots[0].Timestamp))
	})

	It("injectEvent deep-copies the object", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		rvr := makeRVR("test-rvr-0", "Pending")
		to.InjectEvent(watch.Added, rvr)

		rvr.Status.Phase = "Healthy"

		to.mu.RLock()
		defer to.mu.RUnlock()
		Expect(to.snapshots[0].Object).To(match.Phase("Pending"))
	})
})

// ---------------------------------------------------------------------------
// Object()
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject Object()", func() {
	It("returns latest snapshot object", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Expect(to.Object()).To(match.Phase("Healthy"))
	})

	It("fails on empty history", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		err := InterceptGomegaFailure(func() {
			to.Object()
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no snapshots"))
	})

	It("fails on deleted object", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))
		err := InterceptGomegaFailure(func() {
			to.Object()
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deleted"))
	})
})

// ---------------------------------------------------------------------------
// Name()
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject Name()", func() {
	It("returns the configured name", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "my-rvr-0", Lifecycle[*testObj]{})
		Expect(to.Name()).To(Equal("my-rvr-0"))
	})
})

// ---------------------------------------------------------------------------
// Await
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject Await", func() {
	It("returns immediately if latest snapshot matches", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.Await(context.Background(), match.Phase("Healthy"))
		}()

		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("blocks until matching event arrives", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.Await(context.Background(), match.Phase("Healthy"))
		}()

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Pending"))
		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Configuring"))
		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Eventually(done).Should(BeClosed())
	})

	It("handles multiple sequential events before match", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.Await(context.Background(), match.Phase("Healthy"))
		}()

		for _, phase := range []string{"Pending", "Provisioning", "Configuring", "WaitingForDatamesh", "Configuring"} {
			to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", phase))
		}
		Consistently(done, 30*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Eventually(done).Should(BeClosed())
	})

	It("multiple concurrent Awaits all wake on same event", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})

		const n = 5
		done := make([]chan struct{}, n)
		for i := range done {
			done[i] = make(chan struct{})
			go func(ch chan struct{}) {
				defer close(ch)
				to.Await(context.Background(), match.Phase("Healthy"))
			}(done[i])
		}

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))

		for i := range done {
			Eventually(done[i]).Should(BeClosed())
		}
	})
})

// ---------------------------------------------------------------------------
// Await fail-fast on delete
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject Await fail-fast on delete", func() {
	It("fails immediately when object is already deleted", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Pending"))

		to.awaitLoop(context.Background(), match.Phase("Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("deleted while awaiting"))
		to.mu.RUnlock()
	})

	It("fails when Delete arrives while waiting", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.Phase("Healthy"))
		}()

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Configuring"))
		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Configuring"))
		Eventually(done, 100*time.Millisecond).Should(BeClosed())

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("deleted while awaiting"))
		to.mu.RUnlock()
	})
})

// ---------------------------------------------------------------------------
// Thread safety
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject thread safety", func() {
	It("concurrent injectEvent from multiple goroutines", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		const n = 100
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(_ int) {
				defer wg.Done()
				to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Pending"))
			}(i)
		}
		wg.Wait()
		Expect(to.snapshotCount()).To(Equal(n))
	})

	It("concurrent injectEvent + Object reads", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Pending"))

		const n = 100
		var wg sync.WaitGroup
		wg.Add(2 * n)

		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
			}()
			go func() {
				defer wg.Done()
				_ = to.Object()
			}()
		}
		wg.Wait()
	})

	It("concurrent injectEvent + Await", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})

		const awaiters = 10
		done := make([]chan struct{}, awaiters)
		for i := range done {
			done[i] = make(chan struct{})
			go func(ch chan struct{}) {
				defer close(ch)
				to.Await(context.Background(), match.Phase("Healthy"))
			}(done[i])
		}

		for i := 0; i < 50; i++ {
			to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Pending"))
		}
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))

		for i := range done {
			Eventually(done[i]).Should(BeClosed())
		}
	})
})

// ---------------------------------------------------------------------------
// Await with Deleted / Present / PresentAgain
// ---------------------------------------------------------------------------

var _ = Describe("Await with Deleted matcher", func() {
	It("returns immediately if already deleted", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.Deleted())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("blocks until Delete event arrives", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.Deleted())
		}()

		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))
		Eventually(done).Should(BeClosed())
	})
})

var _ = Describe("Await with Present matcher", func() {
	It("returns immediately if object is alive", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.Present())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("fails immediately if object is deleted", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))

		to.awaitLoop(context.Background(), match.Present())

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("deleted while awaiting"))
		to.mu.RUnlock()
	})

	It("succeeds when first event arrives", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.Present())
		}()

		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})
})

var _ = Describe("Await with PresentAgain matcher", func() {
	It("tolerates deleted state and succeeds on reincarnation", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.PresentAgain())
		}()

		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))
		Eventually(done, 100*time.Millisecond).Should(BeClosed())

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("returns immediately if object is alive", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.awaitLoop(context.Background(), match.PresentAgain())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})
})

// ---------------------------------------------------------------------------
// IsDeleted

var _ = Describe("TrackedObject IsDeleted", func() {
	It("returns false when no snapshots", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		Expect(to.IsDeleted()).To(BeFalse())
	})

	It("returns false when last event is not Delete", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Modified, makeRVR("test-rvr-0", "Healthy"))
		Expect(to.IsDeleted()).To(BeFalse())
	})

	It("returns true when last event is Delete", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("test-rvr-0", "Healthy"))
		Expect(to.IsDeleted()).To(BeTrue())
	})
})

var _ = Describe("TrackedObject Object copy isolation", func() {
	It("mutating returned object does not affect subsequent Object calls", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rvr-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("test-rvr-0", "Healthy"))

		obj1 := to.Object()
		obj1.Status.Phase = "MUTATED"

		obj2 := to.Object()
		Expect(obj2).To(match.Phase("Healthy"))
	})
})

// ---------------------------------------------------------------------------
// NewLiteTrackedObject (keepOnlyLast)
// ---------------------------------------------------------------------------

var _ = Describe("NewLiteTrackedObject", func() {
	It("keeps only the latest snapshot", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Configuring"))
		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Healthy"))
		Expect(to.snapshotCount()).To(Equal(1))
	})

	It("Object returns the latest state", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Healthy"))
		Expect(to.Object()).To(match.Phase("Healthy"))
	})

	It("Await works", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.Await(context.Background(), match.Phase("Healthy"))
		}()

		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))
		Consistently(done, 50*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Healthy"))
		Eventually(done).Should(BeClosed())
	})

	It("Always check fires on each event", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})

	It("FollowsFromStart is rejected", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))

		Expect(func() {
			to.FollowsFromStart(match.Phase("Pending"))
		}).To(PanicWith(ContainSubstring("not supported")))
	})

	It("Follows (forward) works", func() {
		to := NewLiteTrackedObject(nil, nil, schema.GroupVersionKind{}, "lite-0", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("lite-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("lite-0", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})
})

// ---------------------------------------------------------------------------
// Reset on recreate (Added after Deleted)
// ---------------------------------------------------------------------------

var _ = Describe("TrackedObject reset on recreate", func() {
	It("IsDeleted returns false after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Healthy"))
		Expect(to.IsDeleted()).To(BeTrue())

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))
		Expect(to.IsDeleted()).To(BeFalse())
	})

	It("SnapshotCount is 1 after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("reset-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Healthy"))
		Expect(to.snapshotCount()).To(Equal(3))

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))
		Expect(to.snapshotCount()).To(Equal(1))
	})

	It("Object returns the new object after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Healthy"))

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))
		Expect(to.Object()).To(match.Phase("Pending"))
	})

	It("checks still fire after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Healthy"))
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeRVR("reset-0", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})

	It("old failedErr is cleared after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Critical"))
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("old sequences are cleared after recreate", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "reset-0", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("reset-0", "Pending"))
		to.InjectEvent(watch.Deleted, makeRVR("reset-0", "Pending"))
		to.InjectEvent(watch.Added, makeRVR("reset-0", "Configuring"))

		to.mu.RLock()
		Expect(to.sequences).To(BeEmpty())
		to.mu.RUnlock()
	})
})
