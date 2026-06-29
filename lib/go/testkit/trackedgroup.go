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

	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Tracked is the constraint for TrackedGroup members. Any type embedding
// *TrackedObject[T] satisfies this automatically via promoted methods.
type Tracked[T client.Object] interface {
	InjectEvent(watch.EventType, T)
	Always(types.GomegaMatcher)
	After(trigger, check types.GomegaMatcher)
	While(condition, check types.GomegaMatcher)
}

// TrackedGroup is a registry of domain members keyed by name.
// Members have stable identity — the same instance is returned on every
// Resolve call for a given name. New members are created via factory.
// OnEach actions are applied to all current and future members.
type TrackedGroup[T client.Object, M Tracked[T]] struct {
	mu           sync.RWMutex
	Cache        cache.Cache
	Client       client.Client
	GVK          schema.GroupVersionKind
	objects      map[string]M
	factory      func(name string) M
	actions      []func(M)
	InformerRegs []InformerReg
}

// NewTrackedGroup creates an empty TrackedGroup with the given factory.
// cache, client, and gvk may be zero in unit tests.
func NewTrackedGroup[T client.Object, M Tracked[T]](
	c cache.Cache, cl client.Client, gvk schema.GroupVersionKind,
	factory func(name string) M,
) *TrackedGroup[T, M] {
	return &TrackedGroup[T, M]{
		Cache:   c,
		Client:  cl,
		GVK:     gvk,
		objects: make(map[string]M),
		factory: factory,
	}
}

// Resolve returns the member for the given name, creating it via factory
// if it doesn't exist. When a new member is created, all queued OnEach
// actions are applied to it.
func (g *TrackedGroup[T, M]) Resolve(name string) M {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.resolveLocked(name)
}

func (g *TrackedGroup[T, M]) resolveLocked(name string) M {
	if m, ok := g.objects[name]; ok {
		return m
	}
	m := g.factory(name)
	g.objects[name] = m
	for _, action := range g.actions {
		action(m)
	}
	return m
}

// InjectEvent routes an event to the named member, creating it lazily
// via factory if needed.
func (g *TrackedGroup[T, M]) InjectEvent(name string, eventType watch.EventType, obj T) {
	g.mu.Lock()
	m := g.resolveLocked(name)
	g.mu.Unlock()
	m.InjectEvent(eventType, obj)
}

// Count returns the number of members in the group.
func (g *TrackedGroup[T, M]) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.objects)
}

// All returns a snapshot of all members in the group.
func (g *TrackedGroup[T, M]) All() []M {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]M, 0, len(g.objects))
	for _, m := range g.objects {
		out = append(out, m)
	}
	return out
}

// Watch registers an informer that filters events with the given predicate
// and routes matching objects into this TrackedGroup.
func (g *TrackedGroup[T, M]) Watch(ctx context.Context, filter func(T) bool) {
	if g.Cache == nil {
		return
	}
	reg := RegisterInformer[T](ctx, g.Cache, g.GVK, "watch group",
		filter,
		func(et watch.EventType, obj T) {
			g.InjectEvent(obj.GetName(), et, obj)
		},
	)
	g.InformerRegs = append(g.InformerRegs, reg)
}

// UnwatchAll deregisters all informer handlers owned by this group.
func (g *TrackedGroup[T, M]) UnwatchAll() {
	RemoveInformerRegs(g.InformerRegs)
	g.InformerRegs = nil
}

// OnEach returns a TrackedGroupHandle for applying actions to all current
// and future members of the group.
func (g *TrackedGroup[T, M]) OnEach() *TrackedGroupHandle[T, M] {
	return &TrackedGroupHandle[T, M]{group: g}
}

// TrackedGroupHandle provides methods that apply to all current and future
// members of a TrackedGroup.
type TrackedGroupHandle[T client.Object, M Tracked[T]] struct {
	group *TrackedGroup[T, M]
}

// Always applies Always(m) to all current and future members.
func (h *TrackedGroupHandle[T, M]) Always(m types.GomegaMatcher) {
	h.group.mu.Lock()
	defer h.group.mu.Unlock()
	action := func(member M) { member.Always(m) }
	for _, member := range h.group.objects {
		action(member)
	}
	h.group.actions = append(h.group.actions, action)
}

// After applies After(trigger, check) to all current and future members.
func (h *TrackedGroupHandle[T, M]) After(trigger, check types.GomegaMatcher) {
	h.group.mu.Lock()
	defer h.group.mu.Unlock()
	action := func(member M) { member.After(trigger, check) }
	for _, member := range h.group.objects {
		action(member)
	}
	h.group.actions = append(h.group.actions, action)
}

// While applies While(condition, check) to all current and future members.
func (h *TrackedGroupHandle[T, M]) While(condition, check types.GomegaMatcher) {
	h.group.mu.Lock()
	defer h.group.mu.Unlock()
	action := func(member M) { member.While(condition, check) }
	for _, member := range h.group.objects {
		action(member)
	}
	h.group.actions = append(h.group.actions, action)
}
