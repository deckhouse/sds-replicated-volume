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

package debug

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Filter", func() {
	var f *Filter

	BeforeEach(func() {
		f = NewFilter()
	})

	Describe("AddKind/RemoveKind", func() {
		It("kind-all watch matches any name", func() {
			f.AddKind("rv")
			Expect(f.MatchesObject("rv", "anything")).To(BeTrue())
		})

		It("unwatched kind does not match", func() {
			f.AddKind("rv")
			Expect(f.MatchesObject("rsc", "anything")).To(BeFalse())
		})

		It("removed kind no longer matches", func() {
			f.AddKind("rv")
			f.RemoveKind("rv")
			Expect(f.MatchesObject("rv", "test")).To(BeFalse())
		})

		It("RemoveKind on non-existent kind does not panic", func() {
			Expect(func() { f.RemoveKind("nonexistent") }).NotTo(Panic())
		})

		It("reference counting: two AddKind, one RemoveKind still matches", func() {
			f.AddKind("rv")
			f.AddKind("rv")
			f.RemoveKind("rv")
			Expect(f.MatchesObject("rv", "anything")).To(BeTrue())
		})

		It("reference counting: two AddKind, two RemoveKind no longer matches", func() {
			f.AddKind("rv")
			f.AddKind("rv")
			f.RemoveKind("rv")
			f.RemoveKind("rv")
			Expect(f.MatchesObject("rv", "anything")).To(BeFalse())
		})

		It("extra RemoveKind beyond zero does not panic or go negative", func() {
			f.AddKind("rv")
			f.RemoveKind("rv")
			f.RemoveKind("rv")
			Expect(f.MatchesObject("rv", "anything")).To(BeFalse())
		})
	})

	Describe("AddName/RemoveName", func() {
		It("specific name matches", func() {
			f.AddName("rv", "my-volume")
			Expect(f.MatchesObject("rv", "my-volume")).To(BeTrue())
			Expect(f.MatchesObject("rv", "other-volume")).To(BeFalse())
		})

		It("multiple names on the same kind", func() {
			f.AddName("rv", "vol-a")
			f.AddName("rv", "vol-b")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeTrue())
			Expect(f.MatchesObject("rv", "vol-b")).To(BeTrue())
			Expect(f.MatchesObject("rv", "vol-c")).To(BeFalse())
		})

		It("removed name no longer matches while sibling remains", func() {
			f.AddName("rv", "vol-a")
			f.AddName("rv", "vol-b")
			f.RemoveName("rv", "vol-a")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeFalse())
			Expect(f.MatchesObject("rv", "vol-b")).To(BeTrue())
		})

		It("removing the last name cleans up byName map entry", func() {
			f.AddName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeFalse())

			f.mu.RLock()
			_, exists := f.byName["rv"]
			f.mu.RUnlock()
			Expect(exists).To(BeFalse())
		})

		It("RemoveName on non-existent kind does not panic", func() {
			Expect(func() { f.RemoveName("nonexistent", "name") }).NotTo(Panic())
		})

		It("reference counting: two AddName, one RemoveName still matches", func() {
			f.AddName("rv", "vol-a")
			f.AddName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeTrue())
		})

		It("reference counting: two AddName, two RemoveName no longer matches", func() {
			f.AddName("rv", "vol-a")
			f.AddName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeFalse())
		})

		It("extra RemoveName beyond zero does not panic or go negative", func() {
			f.AddName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			f.RemoveName("rv", "vol-a")
			Expect(f.MatchesObject("rv", "vol-a")).To(BeFalse())
		})
	})

	Describe("MatchesObject", func() {
		It("kind-all overrides specific names", func() {
			f.AddKind("rv")
			f.AddName("rv", "specific")
			Expect(f.MatchesObject("rv", "anything-else")).To(BeTrue())
		})

		It("empty filter matches nothing", func() {
			Expect(f.MatchesObject("rv", "test")).To(BeFalse())
		})
	})

	Describe("MatchesLog", func() {
		BeforeEach(func() {
			RegisterKind("rv", "ReplicatedVolume")
			RegisterKind("rvr", "ReplicatedVolumeReplica")
		})

		It("matches full kind via ShortKindFor mapping", func() {
			f.AddKind("rv")
			Expect(f.MatchesLog("ReplicatedVolume", "test-rv")).To(BeTrue())
			Expect(f.MatchesLog("rv", "test-rv")).To(BeTrue())
		})

		It("matches specific name via full kind mapping", func() {
			f.AddName("rvr", "my-replica")
			Expect(f.MatchesLog("ReplicatedVolumeReplica", "my-replica")).To(BeTrue())
			Expect(f.MatchesLog("ReplicatedVolumeReplica", "other-replica")).To(BeFalse())
		})

		It("does not match unregistered kinds", func() {
			f.AddKind("rv")
			Expect(f.MatchesLog("UnknownKind", "test")).To(BeFalse())
		})
	})

	Describe("MatchesControllerName", func() {
		It("matches controller by kind prefix", func() {
			f.AddKind("rvr")
			Expect(f.MatchesControllerName("rvr-scheduling-controller")).To(BeTrue())
			Expect(f.MatchesControllerName("rvr-controller")).To(BeTrue())
			Expect(f.MatchesControllerName("rv-controller")).To(BeFalse())
		})

		It("matches controller prefix for name-based watches", func() {
			f.AddName("rv", "test-vol")
			Expect(f.MatchesControllerName("rv-scheduling-controller")).To(BeTrue())
		})

		It("empty filter does not match any controller", func() {
			Expect(f.MatchesControllerName("anything")).To(BeFalse())
		})
	})

	Describe("FilterLogEntry", func() {
		BeforeEach(func() {
			RegisterKind("rv", "ReplicatedVolume")
		})

		It("passes matching object", func() {
			f.AddKind("rv")
			entry := &LogEntry{
				Kind: "ReplicatedVolume",
				Name: "test-vol",
				Raw:  map[string]any{},
			}
			Expect(f.FilterLogEntry(entry)).To(BeTrue())
		})

		It("rejects non-matching object", func() {
			f.AddKind("rv")
			entry := &LogEntry{
				Kind: "SomethingElse",
				Name: "test",
				Raw:  map[string]any{},
			}
			Expect(f.FilterLogEntry(entry)).To(BeFalse())
		})

		It("passes via controller name prefix fallback", func() {
			f.AddKind("rvr")
			entry := &LogEntry{
				Kind:       "SomethingElse",
				Name:       "test",
				Controller: "rvr-scheduling-controller",
				Raw:        map[string]any{},
			}
			Expect(f.FilterLogEntry(entry)).To(BeTrue())
		})

		It("always passes global lifecycle messages", func() {
			entry := &LogEntry{
				Msg: "Starting Controller",
				Raw: map[string]any{},
			}
			Expect(f.FilterLogEntry(entry)).To(BeTrue())
		})

		It("filters out Request Body with no body field", func() {
			f.AddKind("rv")
			entry := &LogEntry{
				Msg: "Request Body",
				Raw: map[string]any{},
			}
			Expect(f.FilterLogEntry(entry)).To(BeFalse())
		})

		It("filters out Response Body with empty body", func() {
			f.AddKind("rv")
			entry := &LogEntry{
				Msg: "Response Body",
				Raw: map[string]any{"body": ""},
			}
			Expect(f.FilterLogEntry(entry)).To(BeFalse())
		})

		It("passes Request Body with data as global message", func() {
			entry := &LogEntry{
				Msg: "Request Body",
				Raw: map[string]any{"body": `{"some":"data"}`},
			}
			Expect(f.FilterLogEntry(entry)).To(BeTrue())
		})
	})

	Describe("allKinds rebuild", func() {
		It("tracks added kinds and names", func() {
			f.AddKind("rv")
			f.AddName("rvr", "test")

			f.mu.RLock()
			count := len(f.allKinds)
			f.mu.RUnlock()
			Expect(count).To(Equal(2))
		})

		It("empties after removing all", func() {
			f.AddKind("rv")
			f.AddName("rvr", "test")

			f.RemoveKind("rv")
			f.RemoveName("rvr", "test")

			f.mu.RLock()
			count := len(f.allKinds)
			f.mu.RUnlock()
			Expect(count).To(BeZero())
		})
	})

	Describe("FilterLogEntry with nil body", func() {
		It("filters out Request Body with body: nil", func() {
			f.AddKind("rv")
			entry := &LogEntry{
				Msg: "Request Body",
				Raw: map[string]any{"body": nil},
			}
			Expect(f.FilterLogEntry(entry)).To(BeFalse())
		})
	})

	Describe("interleaved AddKind + AddName lifecycle", func() {
		It("RemoveKind still matches by name", func() {
			f.AddKind("rv")
			f.AddName("rv", "specific-vol")
			f.RemoveKind("rv")

			Expect(f.MatchesObject("rv", "specific-vol")).To(BeTrue())
			Expect(f.MatchesObject("rv", "other-vol")).To(BeFalse())
		})

		It("RemoveName still matches by kind", func() {
			f.AddKind("rv")
			f.AddName("rv", "specific-vol")
			f.RemoveName("rv", "specific-vol")

			Expect(f.MatchesObject("rv", "specific-vol")).To(BeTrue())
			Expect(f.MatchesObject("rv", "other-vol")).To(BeTrue())
		})
	})

	Describe("concurrent safety", func() {
		It("add/remove/match from multiple goroutines without race", func() {
			kinds := []string{"rv", "rvr", "rsc"}
			names := []string{"obj-a", "obj-b", "obj-c"}
			controllers := []string{"rv-controller", "rvr-scheduling-controller", "rsc-reconciler"}

			done := make(chan struct{})
			time.AfterFunc(200*time.Millisecond, func() { close(done) })

			var wg sync.WaitGroup
			for w := 0; w < 3; w++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for {
						select {
						case <-done:
							return
						default:
						}
						k := kinds[id]
						n := names[id]
						f.AddKind(k)
						f.AddName(k, n)
						f.RemoveName(k, n)
						f.RemoveKind(k)
					}
				}(w)
			}

			for r := 0; r < 3; r++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for {
						select {
						case <-done:
							return
						default:
						}
						f.MatchesObject(kinds[id], names[id])
						f.MatchesLog(kinds[id], names[id])
						f.MatchesControllerName(controllers[id])
					}
				}(r)
			}

			wg.Wait()
		})
	})
})
