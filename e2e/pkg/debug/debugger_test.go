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
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
)

var _ = Describe("stateTracker", func() {
	var st *stateTracker

	BeforeEach(func() {
		st = newStateTracker()
	})

	It("Get returns nil for unknown key", func() {
		key := stateKey{GVK: "rv", Name: "test-vol"}
		Expect(st.Get(key)).To(BeNil())
	})

	It("Set then Get returns state", func() {
		key := stateKey{GVK: "rv", Name: "test-vol"}
		obj := &objectState{
			Lines:      []string{"apiVersion: v1", "kind: Pod"},
			Conditions: []Condition{{Type: "Ready", Status: "True"}},
		}
		st.Set(key, obj)

		got := st.Get(key)
		Expect(got).NotTo(BeNil())
		Expect(got.Lines).To(HaveLen(2))
		Expect(got.Conditions).To(HaveLen(1))
		Expect(got.Conditions[0].Type).To(Equal("Ready"))
	})

	It("Delete removes entry", func() {
		key := stateKey{GVK: "rvr", Name: "replica-0"}
		st.Set(key, &objectState{Lines: []string{"line1"}})
		Expect(st.count()).To(Equal(1))

		st.Delete(key)
		Expect(st.count()).To(Equal(0))
		Expect(st.Get(key)).To(BeNil())
	})

	It("deleteByGVK removes all entries for GVK", func() {
		st.Set(stateKey{GVK: "rv", Name: "vol-1"}, &objectState{Lines: []string{"a"}})
		st.Set(stateKey{GVK: "rv", Name: "vol-2"}, &objectState{Lines: []string{"b"}})
		st.Set(stateKey{GVK: "rvr", Name: "rep-0"}, &objectState{Lines: []string{"c"}})
		Expect(st.count()).To(Equal(3))

		st.deleteByGVK("rv")
		Expect(st.count()).To(Equal(1))
		Expect(st.Get(stateKey{GVK: "rv", Name: "vol-1"})).To(BeNil())
		Expect(st.Get(stateKey{GVK: "rvr", Name: "rep-0"})).NotTo(BeNil())
	})

	It("Clear removes everything", func() {
		st.Set(stateKey{GVK: "rv", Name: "a"}, &objectState{})
		st.Set(stateKey{GVK: "rvr", Name: "b"}, &objectState{})

		st.Clear()
		Expect(st.count()).To(Equal(0))
	})

	It("Set overwrites existing", func() {
		key := stateKey{GVK: "rv", Name: "test"}
		st.Set(key, &objectState{Lines: []string{"old"}})
		st.Set(key, &objectState{Lines: []string{"new"}})

		got := st.Get(key)
		Expect(got).NotTo(BeNil())
		Expect(got.Lines).To(Equal([]string{"new"}))
		Expect(st.count()).To(Equal(1))
	})

	It("Delete non-existent does not panic", func() {
		Expect(func() {
			st.Delete(stateKey{GVK: "rv", Name: "nope"})
		}).NotTo(Panic())
	})

	It("handles concurrent Set/Get/Delete without race", func() {
		const goroutines = 10
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func(n int) {
				defer wg.Done()
				key := stateKey{GVK: "rv", Name: fmt.Sprintf("obj-%d", n)}
				st.Set(key, &objectState{Lines: []string{fmt.Sprintf("line-%d", n)}})
				_ = st.Get(key)
				st.Delete(key)
			}(i)
		}
		wg.Wait()
	})
})

var _ = Describe("Debugger Stop", func() {
	It("removes all watches, clears state and filter", func() {
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rvr", "ReplicatedVolumeReplica")

		var buf bytes.Buffer
		rvInf := &fakeInformer{}
		rvrInf := &fakeInformer{}
		fc := &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK:  rvInf,
				testRVRGVK: rvrInf,
			},
		}
		d := New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")

		obj := newTestObj(testRVGVK, "stop-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())

		d.state.Set(stateKey{GVK: "rv", Name: "stop-vol"}, &objectState{Lines: []string{"data"}})

		Expect(d.filter.MatchesObject("rv", "stop-vol")).To(BeTrue())
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeTrue())

		d.Stop()

		d.mu.Lock()
		watchCount := len(d.watches)
		relCount := len(d.relatedGroups)
		d.mu.Unlock()

		Expect(watchCount).To(Equal(0), "watches should be empty after Stop")
		Expect(relCount).To(Equal(0), "relatedGroups should be empty after Stop")
		Expect(d.state.count()).To(Equal(0), "state should be cleared after Stop")
		Expect(d.filter.MatchesObject("rv", "stop-vol")).To(BeFalse(), "filter should be reset after Stop")
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeFalse(), "filter should be reset after Stop")

		Expect(rvInf.removeCalls).To(Equal(1), "parent informer handler removed")
		Expect(rvrInf.removeCalls).To(Equal(1), "child informer handler removed")
	})

	It("is safe to call Stop on a fresh debugger", func() {
		var buf bytes.Buffer
		fc := &fakeCache{informers: map[schema.GroupVersionKind]*fakeInformer{}}
		d := New(fc, &buf)
		Expect(func() { d.Stop() }).NotTo(Panic())
	})
})

var _ = Describe("Watch refcounting", func() {
	var (
		buf   bytes.Buffer
		fc    *fakeCache
		rvInf *fakeInformer
		d     *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		rvInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK: rvInf,
			},
		}
		d = New(fc, &buf)
		d.RegisterKind("rv", "ReplicatedVolume")
	})

	It("Watch twice, Unwatch once: handler NOT removed, filter still matches", func() {
		obj := newTestObj(testRVGVK, "rc-vol")
		Expect(d.Watch(obj)).To(Succeed())
		Expect(d.Watch(obj)).To(Succeed())

		Expect(rvInf.addCalls).To(Equal(1), "only one AddEventHandler call")

		Expect(d.Unwatch(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(0), "handler should NOT be removed yet")
		Expect(d.filter.MatchesObject("rv", "rc-vol")).To(BeTrue(), "filter should still match")

		d.mu.Lock()
		Expect(d.watches).To(HaveLen(1))
		d.mu.Unlock()
	})

	It("Watch twice, Unwatch twice: handler removed, filter cleared", func() {
		obj := newTestObj(testRVGVK, "rc-vol")
		Expect(d.Watch(obj)).To(Succeed())
		Expect(d.Watch(obj)).To(Succeed())

		Expect(d.Unwatch(obj)).To(Succeed())
		Expect(d.Unwatch(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(1), "handler should be removed")
		Expect(d.filter.MatchesObject("rv", "rc-vol")).To(BeFalse(), "filter should be cleared")

		d.mu.Lock()
		Expect(d.watches).To(BeEmpty())
		d.mu.Unlock()
	})

	It("extra Unwatch beyond zero is a no-op", func() {
		obj := newTestObj(testRVGVK, "rc-vol")
		Expect(d.Watch(obj)).To(Succeed())
		Expect(d.Unwatch(obj)).To(Succeed())
		Expect(d.Unwatch(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(1), "only one RemoveEventHandler call")
	})
})

var _ = Describe("WatchByLabel refcounting", func() {
	var (
		buf    bytes.Buffer
		fc     *fakeCache
		rvrInf *fakeInformer
		d      *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		rvrInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVRGVK: rvrInf,
			},
		}
		d = New(fc, &buf)
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")
	})

	It("two WatchByLabel, one UnwatchByLabel: handler NOT removed", func() {
		Expect(d.WatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())
		Expect(d.WatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())

		Expect(rvrInf.addCalls).To(Equal(1))

		Expect(d.UnwatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())

		Expect(rvrInf.removeCalls).To(Equal(0))
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeTrue())
	})

	It("two WatchByLabel, two UnwatchByLabel: handler removed", func() {
		Expect(d.WatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())
		Expect(d.WatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())

		Expect(d.UnwatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())
		Expect(d.UnwatchByLabel(testRVRGVK, "rv-label=vol-a")).To(Succeed())

		Expect(rvrInf.removeCalls).To(Equal(1))
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeFalse())
	})
})

var _ = Describe("WithEmitter option", func() {
	It("replaces the default emitter", func() {
		var primary, plain bytes.Buffer
		customEmitter := NewWriterEmitter(&primary, &plain)

		rvInf := &fakeInformer{}
		fc := &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{testRVGVK: rvInf},
		}

		var defaultBuf bytes.Buffer
		d := New(fc, &defaultBuf, WithEmitter(customEmitter))

		obj := newTestObj(testRVGVK, "emitter-vol")
		Expect(d.Watch(obj)).To(Succeed())

		st := d.state
		st.Set(stateKey{GVK: "rv", Name: "x"}, &objectState{Lines: []string{"a"}})

		Expect(defaultBuf.Len()).To(Equal(0), "default writer should not be used when WithEmitter is provided")
	})
})

var _ = Describe("eventHandler", func() {
	var (
		buf     bytes.Buffer
		em      *WriterEmitter
		st      *stateTracker
		handler *eventHandler
	)

	matchAll := func(_, _ string) bool { return true }

	BeforeEach(func() {
		buf.Reset()
		em = NewWriterEmitter(&buf, nil)
		st = newStateTracker()
		handler = &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			matchFunc: matchAll,
		}
	})

	It("ADDED emits header + YAML body", func() {
		obj := map[string]any{
			"apiVersion": "storage.deckhouse.io/v1alpha1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
			"spec":       map[string]any{"size": "10Gi"},
		}

		handler.OnAdd(obj, false)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("ADDED"))
		Expect(clean).To(ContainSubstring("[rv]"))
		Expect(clean).To(ContainSubstring("test-vol"))
		Expect(clean).To(ContainSubstring("┌"))
		Expect(clean).To(ContainSubstring("└"))
		Expect(st.Get(stateKey{GVK: "rv", Name: "test-vol"})).NotTo(BeNil())
	})

	It("ADDED with conditions emits conditions table", func() {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol", "generation": float64(1)},
			"spec":       map[string]any{"size": "10Gi"},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"reason":             "OK",
						"observedGeneration": float64(1),
					},
				},
			},
		}

		handler.OnAdd(obj, false)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("conditions"))
		Expect(clean).To(ContainSubstring("Ready"))
		Expect(clean).To(ContainSubstring("├──"))
	})

	It("MODIFIED emits diff for changed fields", func() {
		initialObj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
			"spec":       map[string]any{"size": "10Gi"},
		}
		RemoveConditions(initialObj)
		st.Set(stateKey{GVK: "rv", Name: "test-vol"}, &objectState{
			Lines:      PrettyLines(initialObj),
			Conditions: nil,
		})

		buf.Reset()
		modifiedObj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
			"spec":       map[string]any{"size": "20Gi"},
		}

		handler.OnUpdate(nil, modifiedObj)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("MODIFIED"))
		Expect(clean).To(ContainSubstring("test-vol"))
	})

	It("MODIFIED with diff body shows +/- lines", func() {
		initial := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "diff-vol"},
			"status":     map[string]any{"phase": "Pending"},
		}
		initialCopy := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "diff-vol"},
			"status":     map[string]any{"phase": "Pending"},
		}
		RemoveConditions(initialCopy)
		st.Set(stateKey{GVK: "rv", Name: "diff-vol"}, &objectState{
			Lines:      PrettyLines(initialCopy),
			Conditions: ExtractConditions(initial),
		})

		buf.Reset()
		modified := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "diff-vol"},
			"status":     map[string]any{"phase": "Healthy"},
		}
		handler.OnUpdate(nil, modified)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("MODIFIED"))

		hasMinus := false
		hasPlus := false
		hasPath := false
		for _, line := range strings.Split(clean, "\n") {
			afterBar := line
			if idx := strings.Index(line, "│"); idx >= 0 {
				afterBar = strings.TrimSpace(line[idx+len("│"):])
			}
			if strings.HasPrefix(afterBar, "-") && strings.Contains(afterBar, "Pending") {
				hasMinus = true
			}
			if strings.HasPrefix(afterBar, "+") && strings.Contains(afterBar, "Healthy") {
				hasPlus = true
			}
			if strings.Contains(line, "── status") {
				hasPath = true
			}
		}
		Expect(hasMinus).To(BeTrue(), "expected a '-' line containing 'Pending'")
		Expect(hasPlus).To(BeTrue(), "expected a '+' line containing 'Healthy'")
		Expect(hasPath).To(BeTrue(), "expected YAML path breadcrumb '── status'")
	})

	It("MODIFIED conditions-only change shows table without diff", func() {
		baseSpec := map[string]any{"size": "10Gi"}

		initial := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "cond-vol", "generation": float64(1)},
			"spec":       baseSpec,
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"reason":             "OK",
						"observedGeneration": float64(1),
					},
				},
			},
		}
		oldConds := ExtractConditions(initial)
		RemoveConditions(initial)
		st.Set(stateKey{GVK: "rv", Name: "cond-vol"}, &objectState{
			Lines:      PrettyLines(initial),
			Conditions: oldConds,
		})

		buf.Reset()
		modified := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "cond-vol", "generation": float64(1)},
			"spec":       baseSpec,
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "False",
						"reason":             "Degraded",
						"observedGeneration": float64(1),
					},
				},
			},
		}
		handler.OnUpdate(nil, modified)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("~"))
		Expect(clean).To(ContainSubstring("conditions"))

		for _, line := range strings.Split(clean, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
				if !strings.Contains(trimmed, "conditions") &&
					!strings.Contains(line, "│") {
					Fail("unexpected YAML diff line (only conditions should change): " + trimmed)
				}
			}
		}
	})

	It("MODIFIED no change emits nothing", func() {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
			"spec":       map[string]any{"size": "10Gi"},
		}
		objCopy := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
			"spec":       map[string]any{"size": "10Gi"},
		}
		RemoveConditions(objCopy)
		st.Set(stateKey{GVK: "rv", Name: "test-vol"}, &objectState{
			Lines:      PrettyLines(objCopy),
			Conditions: nil,
		})

		buf.Reset()
		handler.OnUpdate(nil, obj)

		Expect(buf.Len()).To(Equal(0))
	})

	It("DELETED emits header and removes state", func() {
		key := stateKey{GVK: "rv", Name: "test-vol"}
		st.Set(key, &objectState{Lines: []string{"some: yaml"}})

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "test-vol"},
		}

		handler.OnDelete(obj)

		clean := stripAnsi(buf.String())
		Expect(clean).To(ContainSubstring("DELETED"))
		Expect(clean).To(ContainSubstring("test-vol"))
		Expect(st.Get(key)).To(BeNil())
	})

	It("filtered object produces no output", func() {
		handler.matchFunc = func(_, name string) bool {
			return name == "watched-vol"
		}

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "other-vol"},
		}

		handler.OnAdd(obj, false)

		Expect(buf.Len()).To(Equal(0))
	})

	It("BOOKMARK is ignored", func() {
		bookmark := map[string]any{
			"metadata": map[string]any{},
		}

		handler.OnAdd(bookmark, false)

		Expect(buf.Len()).To(Equal(0))
		Expect(st.count()).To(Equal(0))
	})

	It("nil metadata produces no output", func() {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
		}

		handler.OnAdd(obj, false)

		Expect(buf.Len()).To(Equal(0))
		Expect(st.count()).To(Equal(0))
	})

	It("saves snapshot and emits OSC 8 hyperlink", func() {
		tmpDir := GinkgoT().TempDir()
		handler.snapshotsDir = tmpDir

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "snap-vol"},
			"spec":       map[string]any{"size": "5Gi"},
		}

		handler.OnAdd(obj, false)

		entries, err := os.ReadDir(tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())

		found := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "rv-snap-vol-") && strings.HasSuffix(e.Name(), ".yaml") {
				found = true
				content, readErr := os.ReadFile(filepath.Join(tmpDir, e.Name()))
				Expect(readErr).NotTo(HaveOccurred())
				Expect(content).NotTo(BeEmpty())
			}
		}
		Expect(found).To(BeTrue(), "no snapshot file matching rv-snap-vol-*.yaml found")

		raw := buf.String()
		Expect(raw).To(ContainSubstring("\033]8;;"))
	})
})

var _ = Describe("toUnstructuredMap", func() {
	It("nil returns nil", func() {
		Expect(toUnstructuredMap(nil)).To(BeNil())
	})

	It("map returns as-is", func() {
		input := map[string]any{
			"apiVersion": "v1",
			"metadata":   map[string]any{"name": "test"},
		}
		m := toUnstructuredMap(input)
		Expect(m).NotTo(BeNil())
		Expect(m["apiVersion"]).To(Equal("v1"))
	})

	It("unwraps DeletedFinalStateUnknown tombstone", func() {
		inner := map[string]any{
			"apiVersion": "v1",
			"metadata":   map[string]any{"name": "tombstone-obj"},
		}
		tombstone := toolscache.DeletedFinalStateUnknown{
			Key: "default/tombstone-obj",
			Obj: inner,
		}
		m := toUnstructuredMap(tombstone)
		Expect(m).NotTo(BeNil())
		Expect(m["apiVersion"]).To(Equal("v1"))
		meta := m["metadata"].(map[string]any)
		Expect(meta["name"]).To(Equal("tombstone-obj"))
	})

	It("returns nil for tombstone with nil Obj", func() {
		tombstone := toolscache.DeletedFinalStateUnknown{
			Key: "default/gone",
			Obj: nil,
		}
		Expect(toUnstructuredMap(tombstone)).To(BeNil())
	})

	It("returns nil for un-marshallable object", func() {
		Expect(toUnstructuredMap(make(chan int))).To(BeNil())
	})
})

var _ = Describe("eventHandler objectMatchFunc", func() {
	var (
		buf     bytes.Buffer
		em      *WriterEmitter
		st      *stateTracker
		handler *eventHandler
	)

	BeforeEach(func() {
		buf.Reset()
		em = NewWriterEmitter(&buf, nil)
		st = newStateTracker()
	})

	It("matching objectMatchFunc allows the event through", func() {
		handler = &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			objectMatchFunc: func(raw map[string]any) bool {
				labels, _ := raw["metadata"].(map[string]any)["labels"].(map[string]any)
				return labels["app"] == "myapp"
			},
			matchFunc: func(_, _ string) bool { return true },
		}

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata": map[string]any{
				"name":   "matched-vol",
				"labels": map[string]any{"app": "myapp"},
			},
		}
		handler.OnAdd(obj, false)

		Expect(stripAnsi(buf.String())).To(ContainSubstring("ADDED"))
		Expect(stripAnsi(buf.String())).To(ContainSubstring("matched-vol"))
	})

	It("non-matching objectMatchFunc suppresses the event", func() {
		handler = &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			objectMatchFunc: func(_ map[string]any) bool {
				return false
			},
			matchFunc: func(_, _ string) bool { return true },
		}

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "suppressed-vol"},
		}
		handler.OnAdd(obj, false)

		Expect(buf.Len()).To(Equal(0))
	})
})

var _ = Describe("eventHandler ADDED→DELETED→re-ADDED lifecycle", func() {
	var (
		buf     bytes.Buffer
		em      *WriterEmitter
		st      *stateTracker
		handler *eventHandler
	)

	matchAll := func(_, _ string) bool { return true }

	BeforeEach(func() {
		buf.Reset()
		em = NewWriterEmitter(&buf, nil)
		st = newStateTracker()
		handler = &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			matchFunc: matchAll,
		}
	})

	It("re-add after delete emits ADDED, not MODIFIED", func() {
		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle-vol"},
			"spec":       map[string]any{"size": "10Gi"},
		}

		handler.OnAdd(obj, false)
		output1 := stripAnsi(buf.String())
		Expect(output1).To(ContainSubstring("ADDED"))
		Expect(output1).To(ContainSubstring("lifecycle-vol"))

		buf.Reset()
		handler.OnDelete(obj)
		output2 := stripAnsi(buf.String())
		Expect(output2).To(ContainSubstring("DELETED"))
		Expect(st.Get(stateKey{GVK: "rv", Name: "lifecycle-vol"})).To(BeNil())

		buf.Reset()
		newObj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle-vol"},
			"spec":       map[string]any{"size": "20Gi"},
		}
		handler.OnAdd(newObj, false)
		output3 := stripAnsi(buf.String())
		Expect(output3).To(ContainSubstring("ADDED"))
		Expect(output3).NotTo(ContainSubstring("MODIFIED"))
	})
})

var _ = Describe("eventHandler OnAdd with isInInitialList", func() {
	It("isInInitialList=true still emits ADDED", func() {
		var buf bytes.Buffer
		em := NewWriterEmitter(&buf, nil)
		st := newStateTracker()
		handler := &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			matchFunc: func(_, _ string) bool { return true },
		}

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "initial-vol"},
		}
		handler.OnAdd(obj, true)

		Expect(stripAnsi(buf.String())).To(ContainSubstring("ADDED"))
		Expect(stripAnsi(buf.String())).To(ContainSubstring("initial-vol"))
	})
})

var _ = Describe("eventHandler full lifecycle", func() {
	It("Add→Modify spec→Modify conds only→Modify no-change→Delete→re-Add", func() {
		var buf bytes.Buffer
		em := NewWriterEmitter(&buf, nil)
		st := newStateTracker()
		handler := &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			matchFunc: func(_, _ string) bool { return true },
		}

		// Step 1: ADDED
		obj1 := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle", "generation": float64(1)},
			"spec":       map[string]any{"size": "10Gi"},
		}
		handler.OnAdd(obj1, false)
		out := stripAnsi(buf.String())
		Expect(out).To(ContainSubstring("ADDED"))
		Expect(out).To(ContainSubstring("lifecycle"))
		Expect(st.Get(stateKey{GVK: "rv", Name: "lifecycle"})).NotTo(BeNil())

		// Step 2: MODIFIED (spec change)
		buf.Reset()
		obj2 := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle", "generation": float64(1)},
			"spec":       map[string]any{"size": "20Gi"},
		}
		handler.OnUpdate(nil, obj2)
		out = stripAnsi(buf.String())
		Expect(out).To(ContainSubstring("MODIFIED"))
		Expect(out).To(ContainSubstring("20Gi"))

		// Step 3: MODIFIED (conditions only)
		buf.Reset()
		obj3 := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle", "generation": float64(2)},
			"spec":       map[string]any{"size": "20Gi"},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True", "reason": "OK", "observedGeneration": float64(2)},
				},
			},
		}
		handler.OnUpdate(nil, obj3)
		out = stripAnsi(buf.String())
		Expect(out).To(ContainSubstring("MODIFIED"))
		Expect(out).To(ContainSubstring("conditions"))

		// Step 4: MODIFIED (no change — should emit nothing)
		buf.Reset()
		obj4 := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle", "generation": float64(2)},
			"spec":       map[string]any{"size": "20Gi"},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True", "reason": "OK", "observedGeneration": float64(2)},
				},
			},
		}
		handler.OnUpdate(nil, obj4)
		Expect(buf.Len()).To(Equal(0), "no-change update should emit nothing")

		// Step 5: DELETED
		buf.Reset()
		handler.OnDelete(obj4)
		out = stripAnsi(buf.String())
		Expect(out).To(ContainSubstring("DELETED"))
		Expect(st.Get(stateKey{GVK: "rv", Name: "lifecycle"})).To(BeNil())

		// Step 6: re-ADDED (should emit ADDED, not MODIFIED)
		buf.Reset()
		obj6 := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": "lifecycle", "generation": float64(3)},
			"spec":       map[string]any{"size": "50Gi"},
		}
		handler.OnAdd(obj6, false)
		out = stripAnsi(buf.String())
		Expect(out).To(ContainSubstring("ADDED"))
		Expect(out).NotTo(ContainSubstring("MODIFIED"))
	})
})

var _ = Describe("stateTracker same-key concurrency", func() {
	It("concurrent Set+Get on the same key does not race", func() {
		st := newStateTracker()
		key := stateKey{GVK: "rv", Name: "shared-obj"}

		const goroutines = 10
		const iterations = 100

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func(id int) {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					st.Set(key, &objectState{Lines: []string{fmt.Sprintf("g%d-i%d", id, i)}})
					got := st.Get(key)
					Expect(got).NotTo(BeNil())
				}
			}(g)
		}
		wg.Wait()

		final := st.Get(key)
		Expect(final).NotTo(BeNil())
		Expect(final.Lines).To(HaveLen(1))
	})
})

var _ = Describe("eventHandler empty name", func() {
	It("empty metadata.name produces no output", func() {
		var buf bytes.Buffer
		em := NewWriterEmitter(&buf, nil)
		st := newStateTracker()
		handler := &eventHandler{
			emitter:   em,
			formatter: &ColorFormatter{kr: defaultKindRegistry},
			tracker:   st,
			kind:      "rv",
			matchFunc: func(_, _ string) bool { return true },
		}

		obj := map[string]any{
			"apiVersion": "v1",
			"kind":       "ReplicatedVolume",
			"metadata":   map[string]any{"name": ""},
		}
		handler.OnAdd(obj, false)
		Expect(buf.Len()).To(Equal(0))
	})
})
