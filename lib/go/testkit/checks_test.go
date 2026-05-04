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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Always", func() {
	It("fires on every snapshot after activation", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("violation sets failedErr", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("check violated"))
		to.mu.RUnlock()
	})

	It("closes failedCh when violation detected", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))
		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		select {
		case <-to.failedCh:
		case <-time.After(100 * time.Millisecond):
			Fail("failedCh not closed after violation")
		}

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})

	It("error message includes registeredAt", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr.Error()).To(ContainSubstring("Registered at:"))
		Expect(to.failedErr.Error()).To(ContainSubstring("checks_test.go"))
		to.mu.RUnlock()
	})
})

var _ = Describe("After", func() {
	It("dormant until trigger fires", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.After(match.Phase("Healthy"), match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("active forever once triggered", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.After(match.Phase("Healthy"), match.PhaseNot("Critical"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})

	It("check runs on the snapshot that triggers", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.After(match.Phase("Healthy"), match.Phase("Healthy"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})
})

var _ = Describe("While", func() {
	It("fires only when condition is true", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.While(match.Phase("Healthy"), match.HasFinalizer("test-fin"))

		rvr := makeTestObjWithFinalizer("chk-rvr", "Healthy", "test-fin")
		to.InjectEvent(watch.Modified, rvr)

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("skips when condition is false", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.While(match.Phase("Healthy"), match.HasFinalizer("test-fin"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("fires violation when condition true but check fails", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.While(match.Phase("Healthy"), match.HasFinalizer("test-fin"))

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})
})

var _ = Describe("Switch-based suspension", func() {
	It("disabled Switch passes all checks", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		sw := match.NewSwitch(match.PhaseNot("Critical"))
		to.Always(sw)

		sw.Disable()
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})

	It("re-enabled Switch catches violations", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		sw := match.NewSwitch(match.PhaseNot("Critical"))
		to.Always(sw)

		sw.Disable()
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		sw.Enable()
		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		to.mu.RUnlock()
	})

	It("WithDisabled scope-based disable/enable", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		sw := match.NewSwitch(match.PhaseNot("Critical"))
		to.Always(sw)

		match.WithDisabled(sw, func() {
			to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))
		})

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		Expect(sw.IsDisabled()).To(BeFalse())
	})

	It("events during disable are not checked after re-enable", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		sw := match.NewSwitch(match.PhaseNot("Critical"))
		to.Always(sw)

		match.WithDisabled(sw, func() {
			to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))
		})

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Healthy"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})
})

var _ = Describe("Multiple checks", func() {
	It("first violation wins", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))
		to.Always(match.PhaseNot("Degraded"))

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).NotTo(BeNil())
		Expect(to.failedErr.Error()).To(ContainSubstring("Critical"))
		to.mu.RUnlock()
	})
})

var _ = Describe("While toggle", func() {
	It("reactivates when condition becomes true again", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.While(match.Phase("Healthy"), match.HasFinalizer("test-fin"))

		to.InjectEvent(watch.Added, makeTestObjWithFinalizer("chk-rvr", "Healthy", "test-fin"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeRVR("chk-rvr", "Pending"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeTestObjWithFinalizer("chk-rvr", "Healthy", "test-fin"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})
})

var _ = Describe("While with Gomega combinator", func() {
	It("works with Gomega Not as condition", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.While(Not(match.Phase("Pending")), match.HasFinalizer("test-fin"))

		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Pending"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()

		to.InjectEvent(watch.Modified, makeTestObjWithFinalizer("chk-rvr", "Healthy", "test-fin"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})
})

var _ = Describe("Deletion auto-suspend", func() {
	It("suspends all checks on Delete event", func() {
		to := NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "chk-rvr", Lifecycle[*testObj]{})
		to.Always(match.PhaseNot("Critical"))
		to.InjectEvent(watch.Added, makeRVR("chk-rvr", "Healthy"))

		to.InjectEvent(watch.Deleted, makeRVR("chk-rvr", "Critical"))

		to.mu.RLock()
		Expect(to.failedErr).To(BeNil())
		to.mu.RUnlock()
	})
})
