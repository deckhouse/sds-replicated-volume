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
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

type testMember struct {
	*TrackedObject[*testObj]
}

func testMemberFactory(name string) *testMember {
	return &testMember{
		TrackedObject: NewTrackedObject(nil, nil, schema.GroupVersionKind{}, name, Lifecycle[*testObj]{}),
	}
}

func newTestGroup() *TrackedGroup[*testObj, *testMember] {
	return NewTrackedGroup(nil, nil, schema.GroupVersionKind{}, testMemberFactory)
}

var _ = Describe("TrackedGroup", func() {
	Describe("Resolve", func() {
		It("creates new member", func() {
			g := newTestGroup()
			m := g.Resolve("rvr-0")
			Expect(m).NotTo(BeNil())
			Expect(m.Name()).To(Equal("rvr-0"))
		})

		It("returns same member on repeated calls", func() {
			g := newTestGroup()
			m1 := g.Resolve("rvr-0")
			m2 := g.Resolve("rvr-0")
			Expect(m1).To(BeIdenticalTo(m2))
		})

		It("different names create different members", func() {
			g := newTestGroup()
			m1 := g.Resolve("rvr-0")
			m2 := g.Resolve("rvr-1")
			Expect(m1).NotTo(BeIdenticalTo(m2))
		})
	})

	Describe("InjectEvent", func() {
		It("routes to correct member", func() {
			g := newTestGroup()
			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Pending"))
			g.InjectEvent("rvr-1", watch.Added, makeRVR("rvr-1", "Healthy"))

			m0 := g.Resolve("rvr-0")
			m1 := g.Resolve("rvr-1")
			Expect(m0.Object()).To(match.Phase("Pending"))
			Expect(m1.Object()).To(match.Phase("Healthy"))
		})

		It("lazily creates member", func() {
			g := newTestGroup()
			Expect(g.Count()).To(Equal(0))

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Pending"))
			Expect(g.Count()).To(Equal(1))
		})
	})

	Describe("Count and All", func() {
		It("returns correct count", func() {
			g := newTestGroup()
			g.Resolve("rvr-0")
			g.Resolve("rvr-1")
			g.Resolve("rvr-2")
			Expect(g.Count()).To(Equal(3))
		})

		It("All returns all members", func() {
			g := newTestGroup()
			g.Resolve("rvr-0")
			g.Resolve("rvr-1")
			Expect(g.All()).To(HaveLen(2))
		})
	})

	Describe("OnEach", func() {
		It("Always applies to existing members", func() {
			g := newTestGroup()
			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))

			g.OnEach().Always(match.PhaseNot("Critical"))

			m := g.Resolve("rvr-0")
			m.mu.RLock()
			Expect(m.checks).To(HaveLen(1))
			m.mu.RUnlock()
		})

		It("Always applies to future members", func() {
			g := newTestGroup()
			g.OnEach().Always(match.PhaseNot("Critical"))

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))

			m := g.Resolve("rvr-0")
			m.mu.RLock()
			Expect(m.checks).To(HaveLen(1))
			m.mu.RUnlock()
		})

		It("After applies to existing and future", func() {
			g := newTestGroup()
			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Pending"))

			g.OnEach().After(match.Phase("Healthy"), match.PhaseNot("Critical"))

			m0 := g.Resolve("rvr-0")
			m0.mu.RLock()
			Expect(m0.checks).To(HaveLen(1))
			m0.mu.RUnlock()

			g.InjectEvent("rvr-1", watch.Added, makeRVR("rvr-1", "Pending"))
			m1 := g.Resolve("rvr-1")
			m1.mu.RLock()
			Expect(m1.checks).To(HaveLen(1))
			m1.mu.RUnlock()
		})

		It("While applies to existing and future", func() {
			g := newTestGroup()
			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Pending"))

			g.OnEach().While(match.Phase("Healthy"), match.PhaseNot("Critical"))

			m0 := g.Resolve("rvr-0")
			m0.mu.RLock()
			Expect(m0.checks).To(HaveLen(1))
			m0.mu.RUnlock()

			g.InjectEvent("rvr-1", watch.Added, makeRVR("rvr-1", "Pending"))
			m1 := g.Resolve("rvr-1")
			m1.mu.RLock()
			Expect(m1.checks).To(HaveLen(1))
			m1.mu.RUnlock()
		})

		It("checks fire on events through group members", func() {
			g := newTestGroup()
			g.OnEach().Always(match.PhaseNot("Critical"))

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))
			g.InjectEvent("rvr-0", watch.Modified, makeRVR("rvr-0", "Critical"))

			m := g.Resolve("rvr-0")
			m.mu.RLock()
			Expect(m.failedErr).NotTo(BeNil())
			m.mu.RUnlock()
		})

		It("multiple OnEach actions accumulate", func() {
			g := newTestGroup()
			g.OnEach().Always(match.PhaseNot("Critical"))
			g.OnEach().Always(match.PhaseNot("Degraded"))

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))

			m := g.Resolve("rvr-0")
			m.mu.RLock()
			Expect(m.checks).To(HaveLen(2))
			m.mu.RUnlock()
		})
	})

	Describe("Switch with TrackedGroup", func() {
		It("shared Switch disables check on all members", func() {
			g := newTestGroup()
			sw := match.NewSwitch(match.PhaseNot("Critical"))
			g.OnEach().Always(sw)

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))
			g.InjectEvent("rvr-1", watch.Added, makeRVR("rvr-1", "Healthy"))

			match.WithDisabled(sw, func() {
				g.InjectEvent("rvr-0", watch.Modified, makeRVR("rvr-0", "Critical"))
				g.InjectEvent("rvr-1", watch.Modified, makeRVR("rvr-1", "Critical"))
			})

			m0 := g.Resolve("rvr-0")
			m0.mu.RLock()
			Expect(m0.failedErr).To(BeNil())
			m0.mu.RUnlock()

			m1 := g.Resolve("rvr-1")
			m1.mu.RLock()
			Expect(m1.failedErr).To(BeNil())
			m1.mu.RUnlock()

			g.InjectEvent("rvr-0", watch.Modified, makeRVR("rvr-0", "Critical"))
			m0.mu.RLock()
			Expect(m0.failedErr).NotTo(BeNil())
			m0.mu.RUnlock()
		})
	})

	Describe("Stable identity", func() {
		It("member returned by Resolve is the same one that receives events", func() {
			g := newTestGroup()
			m := g.Resolve("rvr-0")

			g.InjectEvent("rvr-0", watch.Added, makeRVR("rvr-0", "Healthy"))

			Expect(m.snapshotCount()).To(Equal(1))
			Expect(m.Object()).To(match.Phase("Healthy"))
		})
	})

	Describe("Thread safety", func() {
		It("concurrent InjectEvent from multiple goroutines", func() {
			g := newTestGroup()
			const n = 50
			var wg sync.WaitGroup
			wg.Add(n)
			for i := 0; i < n; i++ {
				go func(idx int) {
					defer wg.Done()
					name := fmt.Sprintf("rvr-%d", idx%10)
					g.InjectEvent(name, watch.Modified, makeRVR(name, "Healthy"))
				}(i)
			}
			wg.Wait()
			Expect(g.Count()).To(Equal(10))
		})
	})
})
