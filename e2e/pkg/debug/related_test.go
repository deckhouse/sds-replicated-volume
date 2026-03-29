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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// --- test fakes ---

type fakeRegistration struct{}

func (*fakeRegistration) HasSynced() bool { return true }

var _ toolscache.ResourceEventHandlerRegistration = (*fakeRegistration)(nil)

type fakeInformer struct {
	addCalls    int
	removeCalls int
}

func (f *fakeInformer) AddEventHandler(_ toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	f.addCalls++
	return &fakeRegistration{}, nil
}

func (f *fakeInformer) AddEventHandlerWithOptions(handler toolscache.ResourceEventHandler, _ toolscache.HandlerOptions) (toolscache.ResourceEventHandlerRegistration, error) {
	return f.AddEventHandler(handler)
}

func (f *fakeInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, _ time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	return f.AddEventHandler(handler)
}

func (*fakeInformer) AddIndexers(_ toolscache.Indexers) error { return nil }
func (*fakeInformer) HasSynced() bool                         { return true }
func (*fakeInformer) IsStopped() bool                         { return false }

func (f *fakeInformer) RemoveEventHandler(_ toolscache.ResourceEventHandlerRegistration) error {
	f.removeCalls++
	return nil
}

var _ cache.Informer = (*fakeInformer)(nil)

type fakeCache struct {
	informers map[schema.GroupVersionKind]*fakeInformer
}

func (c *fakeCache) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return fmt.Errorf("not implemented")
}

func (c *fakeCache) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return fmt.Errorf("not implemented")
}

func (c *fakeCache) GetInformer(_ context.Context, _ client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *fakeCache) GetInformerForKind(_ context.Context, gvk schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	if inf, ok := c.informers[gvk]; ok {
		return inf, nil
	}
	return nil, fmt.Errorf("no informer for %s", gvk)
}

func (*fakeCache) RemoveInformer(_ context.Context, _ client.Object) error { return nil }
func (*fakeCache) Start(_ context.Context) error                           { return nil }
func (*fakeCache) WaitForCacheSync(_ context.Context) bool                 { return true }

func (*fakeCache) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return nil
}

var _ cache.Cache = (*fakeCache)(nil)

// --- helpers ---

func newTestObj(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	return obj
}

// --- GVK constants ---

var (
	testRVGVK = schema.GroupVersionKind{
		Group:   "storage.deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ReplicatedVolume",
	}
	testRVRGVK = schema.GroupVersionKind{
		Group:   "storage.deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ReplicatedVolumeReplica",
	}
	testRVAGVK = schema.GroupVersionKind{
		Group:   "storage.deckhouse.io",
		Version: "v1alpha1",
		Kind:    "ReplicatedVolumeAttachment",
	}
)

// --- tests ---

var _ = Describe("specFieldMatcher", func() {
	It("matches a top-level field", func() {
		m := specFieldMatcher("name", "my-vol")
		Expect(m(map[string]any{"name": "my-vol"})).To(BeTrue())
	})

	It("returns false for wrong value", func() {
		m := specFieldMatcher("name", "my-vol")
		Expect(m(map[string]any{"name": "other-vol"})).To(BeFalse())
	})

	It("matches a nested field path", func() {
		m := specFieldMatcher("spec.replicatedVolumeName", "my-vol")
		raw := map[string]any{
			"spec": map[string]any{
				"replicatedVolumeName": "my-vol",
			},
		}
		Expect(m(raw)).To(BeTrue())
	})

	It("returns false for missing intermediate key", func() {
		m := specFieldMatcher("spec.replicatedVolumeName", "my-vol")
		Expect(m(map[string]any{"other": map[string]any{}})).To(BeFalse())
	})

	It("returns false for non-map intermediate", func() {
		m := specFieldMatcher("spec.replicatedVolumeName", "my-vol")
		Expect(m(map[string]any{"spec": "not-a-map"})).To(BeFalse())
	})

	It("returns false for non-string leaf value", func() {
		m := specFieldMatcher("spec.count", "3")
		raw := map[string]any{
			"spec": map[string]any{"count": 3},
		}
		Expect(m(raw)).To(BeFalse())
	})

	It("returns false for empty map", func() {
		m := specFieldMatcher("name", "val")
		Expect(m(map[string]any{})).To(BeFalse())
	})

	It("handles deeply nested paths", func() {
		m := specFieldMatcher("a.b.c.d", "deep")
		raw := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": map[string]any{"d": "deep"},
				},
			},
		}
		Expect(m(raw)).To(BeTrue())
	})
})

var _ = Describe("labelMatcher", func() {
	It("matches when label key=value is present", func() {
		m := labelMatcher("app=my-vol")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"app": "my-vol"},
			},
		}
		Expect(m(raw)).To(BeTrue())
	})

	It("returns false for wrong value", func() {
		m := labelMatcher("app=my-vol")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"app": "other-vol"},
			},
		}
		Expect(m(raw)).To(BeFalse())
	})

	It("returns false when label key is missing", func() {
		m := labelMatcher("app=my-vol")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"other": "val"},
			},
		}
		Expect(m(raw)).To(BeFalse())
	})

	It("returns false when metadata has no labels", func() {
		m := labelMatcher("app=my-vol")
		raw := map[string]any{
			"metadata": map[string]any{},
		}
		Expect(m(raw)).To(BeFalse())
	})

	It("handles value containing equals sign", func() {
		m := labelMatcher("key=val=ue")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"key": "val=ue"},
			},
		}
		Expect(m(raw)).To(BeTrue())
	})

	It("empty string selector matches all objects", func() {
		m := labelMatcher("")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"app": "foo"},
			},
		}
		Expect(m(raw)).To(BeTrue())
		Expect(m(map[string]any{})).To(BeTrue())
	})

	It("non-empty selector without equals sign rejects all", func() {
		m := labelMatcher("noequalssign")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"noequalssign": ""},
			},
		}
		Expect(m(raw)).To(BeFalse())
	})

	It("key with empty value matches label with empty value", func() {
		m := labelMatcher("key=")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"key": ""},
			},
		}
		Expect(m(raw)).To(BeTrue())
	})

	It("key with empty value does not match absent label", func() {
		m := labelMatcher("key=")
		raw := map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{},
			},
		}
		Expect(m(raw)).To(BeFalse())
	})
})

var _ = Describe("WatchRelated", func() {
	var (
		buf    bytes.Buffer
		fc     *fakeCache
		rvInf  *fakeInformer
		rvrInf *fakeInformer
		rvaInf *fakeInformer
		d      *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rvr", "ReplicatedVolumeReplica")
		RegisterKind("rva", "ReplicatedVolumeAttachment")

		rvInf = &fakeInformer{}
		rvrInf = &fakeInformer{}
		rvaInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK:  rvInf,
				testRVRGVK: rvrInf,
				testRVAGVK: rvaInf,
			},
		}
	})

	Context("with label-based children", func() {
		BeforeEach(func() {
			d = New(fc, &buf, WithRelationGraph(RelationGraph{
				testRVGVK: {
					{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
				},
			}))
			d.RegisterKind("rv", "ReplicatedVolume")
			d.RegisterKind("rvr", "ReplicatedVolumeReplica")
			d.RegisterKind("rva", "ReplicatedVolumeAttachment")
		})

		It("creates watches for parent and children", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
			Expect(d.watches).To(HaveKey(parentKey))

			childKey := watchKey{gvk: testRVRGVK, labelSelector: "rv-label=test-vol"}
			Expect(d.watches).To(HaveKey(childKey))
		})

		It("records relatedGroups entry with child keys", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
			Expect(d.relatedGroups).To(HaveKey(parentKey))
			entry := d.relatedGroups[parentKey]
			Expect(entry.childKeys).To(HaveLen(1))
			Expect(entry.childKeys[0].labelSelector).To(Equal("rv-label=test-vol"))
			Expect(entry.refCount).To(Equal(1))
		})

		It("updates filter for parent name and child kind", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			Expect(d.filter.MatchesObject("rv", "test-vol")).To(BeTrue())
			Expect(d.filter.MatchesObject("rv", "other-vol")).To(BeFalse())
			Expect(d.filter.MatchesObject("rvr", "any-replica")).To(BeTrue())
		})

		It("calls AddEventHandler on parent and child informers", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			Expect(rvInf.addCalls).To(Equal(1))
			Expect(rvrInf.addCalls).To(Equal(1))
		})
	})

	Context("with spec-field children", func() {
		BeforeEach(func() {
			d = New(fc, &buf, WithRelationGraph(RelationGraph{
				testRVGVK: {
					{GVK: testRVAGVK, Strategy: MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
				},
			}))
			d.RegisterKind("rv", "ReplicatedVolume")
			d.RegisterKind("rva", "ReplicatedVolumeAttachment")
		})

		It("creates watch with specFilter key", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			childKey := watchKey{gvk: testRVAGVK, specFilter: "spec.replicatedVolumeName=test-vol"}
			Expect(d.watches).To(HaveKey(childKey))
		})

		It("records specFilter in relatedGroups child key", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
			entry := d.relatedGroups[parentKey]
			Expect(entry.childKeys).To(HaveLen(1))
			Expect(entry.childKeys[0].specFilter).To(Equal("spec.replicatedVolumeName=test-vol"))
		})

		It("adds child kind to filter", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			Expect(d.filter.MatchesObject("rva", "anything")).To(BeTrue())
		})
	})

	Context("with mixed label and spec-field children", func() {
		BeforeEach(func() {
			d = New(fc, &buf, WithRelationGraph(RelationGraph{
				testRVGVK: {
					{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
					{GVK: testRVAGVK, Strategy: MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
				},
			}))
			d.RegisterKind("rv", "ReplicatedVolume")
			d.RegisterKind("rvr", "ReplicatedVolumeReplica")
			d.RegisterKind("rva", "ReplicatedVolumeAttachment")
		})

		It("creates all watches and records all child keys", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			Expect(d.watches).To(HaveLen(3))
			parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
			Expect(d.relatedGroups[parentKey].childKeys).To(HaveLen(2))
		})
	})

	Context("with no relation graph entry", func() {
		BeforeEach(func() {
			d = New(fc, &buf)
			d.RegisterKind("rv", "ReplicatedVolume")
		})

		It("watches parent only", func() {
			obj := newTestObj(testRVGVK, "test-vol")
			Expect(d.WatchRelated(obj)).To(Succeed())

			d.mu.Lock()
			defer d.mu.Unlock()

			Expect(d.watches).To(HaveLen(1))
			parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
			Expect(d.watches).To(HaveKey(parentKey))
			Expect(d.relatedGroups[parentKey].childKeys).To(BeEmpty())
		})
	})

	It("is idempotent for parent and child watches", func() {
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")

		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())
		Expect(d.WatchRelated(obj)).To(Succeed())

		Expect(rvInf.addCalls).To(Equal(1), "parent informer should be called once")
		Expect(rvrInf.addCalls).To(Equal(1), "child informer should be called once")
	})

	It("WatchRelated twice, UnwatchRelated once: watches remain", func() {
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")

		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())
		Expect(d.WatchRelated(obj)).To(Succeed())

		Expect(d.UnwatchRelated(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(0), "parent handler should NOT be removed yet")
		Expect(rvrInf.removeCalls).To(Equal(0), "child handler should NOT be removed yet")
		Expect(d.filter.MatchesObject("rv", "test-vol")).To(BeTrue())
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeTrue())

		d.mu.Lock()
		Expect(d.watches).To(HaveLen(2))
		Expect(d.relatedGroups).To(HaveLen(1))
		d.mu.Unlock()
	})

	It("WatchRelated twice, UnwatchRelated twice: watches fully removed", func() {
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")

		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())
		Expect(d.WatchRelated(obj)).To(Succeed())

		Expect(d.UnwatchRelated(obj)).To(Succeed())
		Expect(d.UnwatchRelated(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(1), "parent handler removed")
		Expect(rvrInf.removeCalls).To(Equal(1), "child handler removed")
		Expect(d.filter.MatchesObject("rv", "test-vol")).To(BeFalse())
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeFalse())

		d.mu.Lock()
		Expect(d.watches).To(BeEmpty())
		Expect(d.relatedGroups).To(BeEmpty())
		d.mu.Unlock()
	})

	It("returns error for object without GVK", func() {
		d = New(fc, &buf)
		d.RegisterKind("rv", "ReplicatedVolume")
		obj := &unstructured.Unstructured{}
		obj.SetName("test-vol")
		Expect(d.WatchRelated(obj)).To(MatchError(ContainSubstring("no GVK set")))
	})
})

var _ = Describe("UnwatchRelated", func() {
	var (
		buf    bytes.Buffer
		fc     *fakeCache
		rvInf  *fakeInformer
		rvrInf *fakeInformer
		rvaInf *fakeInformer
		d      *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rvr", "ReplicatedVolumeReplica")
		RegisterKind("rva", "ReplicatedVolumeAttachment")

		rvInf = &fakeInformer{}
		rvrInf = &fakeInformer{}
		rvaInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK:  rvInf,
				testRVRGVK: rvrInf,
				testRVAGVK: rvaInf,
			},
		}
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
				{GVK: testRVAGVK, Strategy: MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")
		d.RegisterKind("rva", "ReplicatedVolumeAttachment")
	})

	It("removes all watches and relatedGroups", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())
		Expect(d.UnwatchRelated(obj)).To(Succeed())

		d.mu.Lock()
		defer d.mu.Unlock()

		Expect(d.watches).To(BeEmpty())
		Expect(d.relatedGroups).To(BeEmpty())
	})

	It("calls RemoveEventHandler on all informers", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())
		Expect(d.UnwatchRelated(obj)).To(Succeed())

		Expect(rvInf.removeCalls).To(Equal(1))
		Expect(rvrInf.removeCalls).To(Equal(1))
		Expect(rvaInf.removeCalls).To(Equal(1))
	})

	It("cleans filter entries", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())

		Expect(d.filter.MatchesObject("rv", "test-vol")).To(BeTrue())
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeTrue())
		Expect(d.filter.MatchesObject("rva", "any")).To(BeTrue())

		Expect(d.UnwatchRelated(obj)).To(Succeed())

		Expect(d.filter.MatchesObject("rv", "test-vol")).To(BeFalse())
		Expect(d.filter.MatchesObject("rvr", "any")).To(BeFalse())
		Expect(d.filter.MatchesObject("rva", "any")).To(BeFalse())
	})

	It("cleans parent state eagerly; child state is cleaned lazily", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(Succeed())

		d.state.Set(stateKey{GVK: "rv", Name: "test-vol"}, &objectState{Lines: []string{"a"}})
		d.state.Set(stateKey{GVK: "rvr", Name: "replica-0"}, &objectState{Lines: []string{"b"}})
		d.state.Set(stateKey{GVK: "rva", Name: "attach-0"}, &objectState{Lines: []string{"c"}})

		Expect(d.UnwatchRelated(obj)).To(Succeed())

		Expect(d.state.Get(stateKey{GVK: "rv", Name: "test-vol"})).To(BeNil(), "parent state is cleaned eagerly")
		Expect(d.state.Get(stateKey{GVK: "rvr", Name: "replica-0"})).NotTo(BeNil(), "child state remains until DELETED event")
		Expect(d.state.Get(stateKey{GVK: "rva", Name: "attach-0"})).NotTo(BeNil(), "child state remains until DELETED event")
	})

	It("is a no-op for unwatched object", func() {
		obj := newTestObj(testRVGVK, "unknown-vol")
		Expect(d.UnwatchRelated(obj)).To(Succeed())
	})

	It("returns error for object without GVK", func() {
		obj := &unstructured.Unstructured{}
		obj.SetName("test-vol")
		Expect(d.UnwatchRelated(obj)).To(MatchError(ContainSubstring("no GVK set")))
	})
})

var _ = Describe("two parents sharing child GVK", func() {
	var (
		buf    bytes.Buffer
		fc     *fakeCache
		rvInf  *fakeInformer
		rvrInf *fakeInformer
		d      *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rvr", "ReplicatedVolumeReplica")

		rvInf = &fakeInformer{}
		rvrInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK:  rvInf,
				testRVRGVK: rvrInf,
			},
		}
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")
	})

	It("unwatch first parent keeps filter for second parent's children", func() {
		objA := newTestObj(testRVGVK, "vol-a")
		objB := newTestObj(testRVGVK, "vol-b")
		Expect(d.WatchRelated(objA)).To(Succeed())
		Expect(d.WatchRelated(objB)).To(Succeed())

		Expect(d.filter.MatchesObject("rvr", "any-replica")).To(BeTrue(), "both parents share rvr kind")

		Expect(d.UnwatchRelated(objA)).To(Succeed())

		Expect(d.filter.MatchesObject("rvr", "any-replica")).To(BeTrue(), "second parent still watching rvr")
		Expect(d.filter.MatchesObject("rv", "vol-a")).To(BeFalse(), "first parent name removed")
		Expect(d.filter.MatchesObject("rv", "vol-b")).To(BeTrue(), "second parent name still present")
	})

	It("unwatch first parent does not nuke second parent's child state", func() {
		objA := newTestObj(testRVGVK, "vol-a")
		objB := newTestObj(testRVGVK, "vol-b")
		Expect(d.WatchRelated(objA)).To(Succeed())
		Expect(d.WatchRelated(objB)).To(Succeed())

		d.state.Set(stateKey{GVK: "rvr", Name: "replica-from-b"}, &objectState{Lines: []string{"x"}})

		Expect(d.UnwatchRelated(objA)).To(Succeed())

		Expect(d.state.Get(stateKey{GVK: "rvr", Name: "replica-from-b"})).NotTo(BeNil(),
			"state for second parent's child must survive")
	})

	It("unwatch both parents removes filter for child kind", func() {
		objA := newTestObj(testRVGVK, "vol-a")
		objB := newTestObj(testRVGVK, "vol-b")
		Expect(d.WatchRelated(objA)).To(Succeed())
		Expect(d.WatchRelated(objB)).To(Succeed())

		Expect(d.UnwatchRelated(objA)).To(Succeed())
		Expect(d.UnwatchRelated(objB)).To(Succeed())

		Expect(d.filter.MatchesObject("rvr", "any")).To(BeFalse(), "no more watchers for rvr")
	})
})

var _ = Describe("WatchRelated partial failure", func() {
	var (
		buf    bytes.Buffer
		fc     *fakeCache
		rvInf  *fakeInformer
		rvrInf *fakeInformer
		d      *Debugger
	)

	BeforeEach(func() {
		buf.Reset()
		RegisterKind("rv", "ReplicatedVolume")
		RegisterKind("rvr", "ReplicatedVolumeReplica")
		RegisterKind("rva", "ReplicatedVolumeAttachment")

		rvInf = &fakeInformer{}
		rvrInf = &fakeInformer{}
		fc = &fakeCache{
			informers: map[schema.GroupVersionKind]*fakeInformer{
				testRVGVK:  rvInf,
				testRVRGVK: rvrInf,
			},
		}
		d = New(fc, &buf, WithRelationGraph(RelationGraph{
			testRVGVK: {
				{GVK: testRVRGVK, Strategy: MatchByLabel, LabelKey: "rv-label"},
				{GVK: testRVAGVK, Strategy: MatchBySpecField, SpecFieldPath: "spec.replicatedVolumeName"},
			},
		}))
		d.RegisterKind("rv", "ReplicatedVolume")
		d.RegisterKind("rvr", "ReplicatedVolumeReplica")
		d.RegisterKind("rva", "ReplicatedVolumeAttachment")
	})

	It("records partial relatedGroups so UnwatchRelated can clean up", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		err := d.WatchRelated(obj)
		Expect(err).To(HaveOccurred(), "second child has no informer")
		Expect(err.Error()).To(ContainSubstring("ReplicatedVolumeAttachment"))

		d.mu.Lock()
		parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
		entry := d.relatedGroups[parentKey]
		d.mu.Unlock()

		Expect(entry.childKeys).To(HaveLen(1), "first child was registered successfully")
		Expect(entry.childKeys[0].labelSelector).To(Equal("rv-label=test-vol"))
	})

	It("UnwatchRelated cleans up the partial entry", func() {
		obj := newTestObj(testRVGVK, "test-vol")
		Expect(d.WatchRelated(obj)).To(HaveOccurred())

		Expect(d.UnwatchRelated(obj)).To(Succeed())

		d.mu.Lock()
		defer d.mu.Unlock()

		parentKey := watchKey{gvk: testRVGVK, name: "test-vol"}
		Expect(d.relatedGroups).NotTo(HaveKey(parentKey))
		Expect(d.watches).To(BeEmpty())
	})
})

var _ = Describe("watchKey", func() {
	It("distinguishes name-based keys", func() {
		k1 := watchKey{gvk: testRVGVK, name: "vol-a"}
		k2 := watchKey{gvk: testRVGVK, name: "vol-b"}
		Expect(k1).NotTo(Equal(k2))
	})

	It("distinguishes label-based keys", func() {
		k1 := watchKey{gvk: testRVRGVK, labelSelector: "label=vol-a"}
		k2 := watchKey{gvk: testRVRGVK, labelSelector: "label=vol-b"}
		Expect(k1).NotTo(Equal(k2))
	})

	It("distinguishes specFilter-based keys", func() {
		k1 := watchKey{gvk: testRVAGVK, specFilter: "spec.rvName=vol-a"}
		k2 := watchKey{gvk: testRVAGVK, specFilter: "spec.rvName=vol-b"}
		Expect(k1).NotTo(Equal(k2))
	})

	It("distinguishes name vs label vs specFilter", func() {
		kName := watchKey{gvk: testRVGVK, name: "test"}
		kLabel := watchKey{gvk: testRVGVK, labelSelector: "test"}
		kSpec := watchKey{gvk: testRVGVK, specFilter: "test"}
		Expect(kName).NotTo(Equal(kLabel))
		Expect(kName).NotTo(Equal(kSpec))
		Expect(kLabel).NotTo(Equal(kSpec))
	})

	It("same values produce equal keys", func() {
		k1 := watchKey{gvk: testRVGVK, name: "same"}
		k2 := watchKey{gvk: testRVGVK, name: "same"}
		Expect(k1).To(Equal(k2))
	})
})
