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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Sequence", func() {
	It("completes a correct sequence", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Configuring"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Configuring"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("stays on current step for repeated updates", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("fails on unexpected state", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("sequence failed"))
		to.mu.RUnlock()
	})

	It("tolerates skipped intermediate step (gap tolerance)", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Configuring"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("fails on unknown intermediate state", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("unexpected state"))
		to.mu.RUnlock()
	})

	It("FromStart replays history", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Configuring"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		to.FollowsFromStart(
			match.Phase("Pending"),
			match.Phase("Configuring"),
			match.Phase("Healthy"),
		)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("AwaitSequence waits for multiple sequences", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})

		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)
		to.Follows(
			match.PhaseNot("Healthy"),
			match.Phase("Healthy"),
		)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		Consistently(done, 30*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))
		Eventually(done).Should(BeClosed())
	})

	It("AwaitSequence blocks until done", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		Consistently(done, 30*time.Millisecond).ShouldNot(BeClosed())

		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))
		Eventually(done).Should(BeClosed())
	})

	It("error message includes registeredAt", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr.Error()).To(ContainSubstring("Registered at:"))
		Expect(to.failedErr.Error()).To(ContainSubstring("sequence_test.go"))
		to.mu.RUnlock()
	})

	It("single-step sequence completes immediately", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(match.Phase("Healthy"))

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("completed sequence ignores further events", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("FromStart with violation during replay fails immediately", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Critical"))

		to.FollowsFromStart(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("replay"))
		to.mu.RUnlock()
	})

	It("uses AnyPhase matcher for non-deterministic steps", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.AnyPhase("Provisioning", "Configuring"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Provisioning"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Configuring"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("Delete without explicit step fails sequence", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Pending"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("object deleted while sequence in progress"))
		to.mu.RUnlock()
	})

	It("Delete with Deleted() as last step completes sequence", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Deleted(),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("Deleted() not last step panics at registration", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		Expect(func() {
			to.Follows(
				match.Deleted(),
				match.Phase("Pending"),
			)
		}).To(PanicWith(ContainSubstring("last step")))
	})

	It("normal events skip Deleted() matcher in gap tolerance", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Healthy"),
			match.Deleted(),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("Delete with gap tolerance skips intermediate steps", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Pending"),
			match.Phase("Configuring"),
			match.Phase("Healthy"),
			match.Deleted(),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Pending"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("Deleted followed by PresentAgain completes on reincarnation", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Phase("Healthy"),
			match.Deleted(),
			match.PresentAgain(),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("Present as first step matches on alive object", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.Follows(
			match.Present(),
			match.Phase("Healthy"),
		)

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("registration on deleted object fails if first step is regular matcher", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Healthy"))

		to.Follows(match.Phase("Pending"), match.Phase("Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("object is deleted"))
		to.mu.RUnlock()
	})

	It("registration on deleted object succeeds if first step is PresentAgain", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Healthy"))

		to.Follows(match.PresentAgain(), match.Phase("Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("PresentAgain as first step completes on reincarnation", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Healthy"))
		to.InjectEvent(watch.Deleted, makeRVR("seq-rvr", "Healthy"))

		to.Follows(match.PresentAgain(), match.Phase("Healthy"))

		to.InjectEvent(watch.Added, makeRVR("seq-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("seq-rvr", "Healthy"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			to.AwaitFollowed(context.Background())
		}()
		Eventually(done, 100*time.Millisecond).Should(BeClosed())
	})

	It("validation rejects Deleted followed by Present", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		Expect(func() {
			to.Follows(match.Phase("Healthy"), match.Deleted(), match.Present())
		}).To(PanicWith(ContainSubstring("PresentAgain()")))
	})

	It("validation rejects PresentAgain after non-Deleted step", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		Expect(func() {
			to.Follows(match.Phase("Healthy"), match.PresentAgain())
		}).To(PanicWith(ContainSubstring("preceded by Deleted()")))
	})

	It("validation rejects Deleted followed by regular matcher", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "seq-rvr", Lifecycle[*testObj]{})
		Expect(func() {
			to.Follows(match.Phase("Healthy"), match.Deleted(), match.Phase("Pending"))
		}).To(PanicWith(ContainSubstring("PresentAgain()")))
	})
})
