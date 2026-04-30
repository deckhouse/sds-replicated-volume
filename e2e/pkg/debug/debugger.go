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

// Package debug provides a library for watching Kubernetes resources and
// streaming controller/agent logs with colored diffs, conditions tables,
// and dynamic filtering. It can be used as an imported library (with
// GinkgoWriter) or as a standalone CLI tool.
package debug

import (
	"context"
	"fmt"
	"io"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Debugger watches Kubernetes resources via a shared informer cache and
// emits colored diffs, conditions tables, and event headers to an Emitter.
//
// Watch and Unwatch register/remove event handlers on the shared cache
// informer — no new server-side watches are created. Unwatch is synchronous:
// the handler is removed and confirmed before returning.
type Debugger struct {
	cache         cache.Cache
	emitter       Emitter
	formatter     Formatter
	filter        *Filter
	state         *stateTracker
	logStreamer   *LogStreamer
	kindReg       *KindRegistry
	mu            sync.Mutex
	watches       map[watchKey]watchHandle
	relationGraph RelationGraph
	relatedGroups map[watchKey]*relatedEntry
	snapshotsDir  string
}

// watchKey uniquely identifies a watch registration.
type watchKey struct {
	gvk           schema.GroupVersionKind
	name          string // empty for label-based and spec-filter watches
	labelSelector string // empty for name-based and spec-filter watches
	specFilter    string // empty for name-based and label-based watches
}

// watchHandle holds the informer registration, the short kind name, and a
// reference count. The informer handler is only removed when refCount drops
// to zero.
type watchHandle struct {
	registration toolscache.ResourceEventHandlerRegistration
	informer     cache.Informer
	kind         string // short kind name for display
	refCount     int
}

// relatedEntry groups the child watch keys created by WatchRelated with a
// reference count so that multiple WatchRelated calls for the same parent
// are balanced by the same number of UnwatchRelated calls.
type relatedEntry struct {
	childKeys []watchKey
	refCount  int
}

// Option configures a Debugger.
type Option func(*Debugger)

// WithSnapshots enables saving full object snapshots to the given directory.
// Object names in the terminal become clickable OSC 8 hyperlinks.
func WithSnapshots(dir string) Option {
	return func(d *Debugger) {
		d.snapshotsDir = dir
	}
}

// WithRelationGraph configures the parent→child relation graph used by
// WatchRelated and UnwatchRelated to automatically watch child resources.
func WithRelationGraph(g RelationGraph) Option {
	return func(d *Debugger) {
		d.relationGraph = g
	}
}

// WithEmitter replaces the default Emitter. Use this to provide a
// WriterEmitter that also writes to a plain-text log file.
func WithEmitter(e Emitter) Option {
	return func(d *Debugger) {
		d.emitter = e
	}
}

// WithFormatter replaces the default Formatter. The default is ColorFormatter
// when colors are enabled, or PlainFormatter when NO_COLOR is set.
func WithFormatter(f Formatter) Option {
	return func(d *Debugger) {
		d.formatter = f
	}
}

// renderConfigFor derives a RenderConfig from a Formatter. PlainFormatter
// disables colors; all other formatters follow the global ColorsEnabled state.
func renderConfigFor(f Formatter) RenderConfig {
	if _, ok := f.(*PlainFormatter); ok {
		return RenderConfig{Colors: false}
	}
	return RenderConfig{Colors: ColorsEnabled}
}

// New creates a Debugger that writes to the given output writer. The cache
// must be started and synced before calling Watch.
func New(c cache.Cache, output io.Writer, opts ...Option) *Debugger {
	kr := NewKindRegistry()
	var defaultFormatter Formatter
	if ColorsEnabled {
		defaultFormatter = &ColorFormatter{kr: kr}
	} else {
		defaultFormatter = &PlainFormatter{kr: kr}
	}
	d := &Debugger{
		cache:         c,
		emitter:       NewWriterEmitter(output, nil),
		formatter:     defaultFormatter,
		filter:        newFilter(kr),
		state:         newStateTracker(),
		kindReg:       kr,
		watches:       make(map[watchKey]watchHandle),
		relatedGroups: make(map[watchKey]*relatedEntry),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Watch starts watching a specific named object. The GVK must be set on the
// object (via SetGroupVersionKind). The event handler is filtered to only
// process events for the given object name.
//
// Multiple calls for the same object increment a reference count; the
// informer handler is only removed when the count drops to zero via Unwatch.
func (d *Debugger) Watch(obj client.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("object has no GVK set; call SetGroupVersionKind first")
	}

	kind := d.kindReg.ShortFor(gvk.Kind)
	name := obj.GetName()
	key := watchKey{gvk: gvk, name: name}

	d.mu.Lock()
	defer d.mu.Unlock()

	if h, exists := d.watches[key]; exists {
		h.refCount++
		d.watches[key] = h
		d.filter.AddName(kind, name)
		return nil
	}

	informer, err := d.cache.GetInformerForKind(context.Background(), gvk)
	if err != nil {
		return fmt.Errorf("get informer for %s: %w", gvk, err)
	}

	handler := &eventHandler{
		emitter:      d.emitter,
		formatter:    d.formatter,
		tracker:      d.state,
		kind:         kind,
		snapshotsDir: d.snapshotsDir,
		renderCfg:    renderConfigFor(d.formatter),
		kindReg:      d.kindReg,
		matchFunc: func(_, objName string) bool {
			return objName == name
		},
	}

	reg, err := informer.AddEventHandler(handler)
	if err != nil {
		return fmt.Errorf("add event handler for %s/%s: %w", kind, name, err)
	}

	d.watches[key] = watchHandle{
		registration: reg,
		informer:     informer,
		kind:         kind,
		refCount:     1,
	}

	d.filter.AddName(kind, name)
	return nil
}

// Unwatch stops watching a specific named object. The event handler is
// removed synchronously — no further events will be processed for this
// object after Unwatch returns. The object's state is also removed.
func (d *Debugger) Unwatch(obj client.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("object has no GVK set; call SetGroupVersionKind first")
	}
	return d.unwatchByKey(watchKey{gvk: gvk, name: obj.GetName()})
}

// WatchByLabel starts watching all objects of the given GVK that match the
// label selector. Filtering is client-side: the informer delivers all events
// for the GVK, and the handler passes all of them through (the informer
// cache applies the label selector at the server-side watch level if
// configured in cache.Options).
//
// Multiple calls for the same GVK + label selector increment a reference
// count; the informer handler is only removed when the count drops to zero.
func (d *Debugger) WatchByLabel(gvk schema.GroupVersionKind, labelSelector string) error {
	kind := d.kindReg.ShortFor(gvk.Kind)
	key := watchKey{gvk: gvk, labelSelector: labelSelector}

	d.mu.Lock()
	defer d.mu.Unlock()

	if h, exists := d.watches[key]; exists {
		h.refCount++
		d.watches[key] = h
		d.filter.AddKind(kind)
		return nil
	}

	informer, err := d.cache.GetInformerForKind(context.Background(), gvk)
	if err != nil {
		return fmt.Errorf("get informer for %s: %w", gvk, err)
	}

	handler := &eventHandler{
		emitter:         d.emitter,
		formatter:       d.formatter,
		tracker:         d.state,
		kind:            kind,
		snapshotsDir:    d.snapshotsDir,
		renderCfg:       renderConfigFor(d.formatter),
		kindReg:         d.kindReg,
		objectMatchFunc: labelMatcher(labelSelector),
		matchFunc:       func(_, _ string) bool { return true },
	}

	reg, err := informer.AddEventHandler(handler)
	if err != nil {
		return fmt.Errorf("add event handler for %s (label=%s): %w", kind, labelSelector, err)
	}

	d.watches[key] = watchHandle{
		registration: reg,
		informer:     informer,
		kind:         kind,
		refCount:     1,
	}

	d.filter.AddKind(kind)
	return nil
}

// UnwatchByLabel stops watching objects matching the given GVK and label
// selector. The event handler is removed synchronously.
func (d *Debugger) UnwatchByLabel(gvk schema.GroupVersionKind, labelSelector string) error {
	return d.unwatchByKey(watchKey{gvk: gvk, labelSelector: labelSelector})
}

// unwatchByKey decrements the reference count for the given watch key.
// When the count drops to zero, the event handler is removed from the
// informer and the state tracker is cleaned up. Filter counters are
// decremented on every call. This is the shared implementation behind
// Unwatch, UnwatchByLabel, and UnwatchRelated.
func (d *Debugger) unwatchByKey(key watchKey) error {
	d.mu.Lock()
	handle, exists := d.watches[key]
	if !exists {
		d.mu.Unlock()
		return nil
	}

	handle.refCount--
	if handle.refCount > 0 {
		d.watches[key] = handle
		d.mu.Unlock()

		switch {
		case key.name != "":
			d.filter.RemoveName(handle.kind, key.name)
		case key.labelSelector != "" || key.specFilter != "":
			d.filter.RemoveKind(handle.kind)
		}
		return nil
	}

	delete(d.watches, key)
	d.mu.Unlock()

	if err := handle.informer.RemoveEventHandler(handle.registration); err != nil {
		return fmt.Errorf("remove event handler for %s: %w", handle.kind, err)
	}

	switch {
	case key.name != "":
		d.filter.RemoveName(handle.kind, key.name)
		d.state.Delete(stateKey{GVK: handle.kind, Name: key.name})
	case key.labelSelector != "" || key.specFilter != "":
		d.filter.RemoveKind(handle.kind)
	}

	return nil
}

// watchBySpecField starts watching all objects of the given GVK where the
// spec field at fieldPath equals value. Filtering is client-side via
// objectMatchFunc on the event handler.
//
// Multiple calls for the same GVK + field path + value increment a reference
// count; the informer handler is only removed when the count drops to zero.
func (d *Debugger) watchBySpecField(gvk schema.GroupVersionKind, fieldPath, value string) error {
	kind := d.kindReg.ShortFor(gvk.Kind)
	key := watchKey{gvk: gvk, specFilter: fieldPath + "=" + value}

	d.mu.Lock()
	defer d.mu.Unlock()

	if h, exists := d.watches[key]; exists {
		h.refCount++
		d.watches[key] = h
		d.filter.AddKind(kind)
		return nil
	}

	informer, err := d.cache.GetInformerForKind(context.Background(), gvk)
	if err != nil {
		return fmt.Errorf("get informer for %s: %w", gvk, err)
	}

	handler := &eventHandler{
		emitter:         d.emitter,
		formatter:       d.formatter,
		tracker:         d.state,
		kind:            kind,
		snapshotsDir:    d.snapshotsDir,
		renderCfg:       renderConfigFor(d.formatter),
		kindReg:         d.kindReg,
		objectMatchFunc: specFieldMatcher(fieldPath, value),
		matchFunc:       func(_, _ string) bool { return true },
	}

	reg, err := informer.AddEventHandler(handler)
	if err != nil {
		return fmt.Errorf("add event handler for %s (specFilter=%s=%s): %w", kind, fieldPath, value, err)
	}

	d.watches[key] = watchHandle{
		registration: reg,
		informer:     informer,
		kind:         kind,
		refCount:     1,
	}

	d.filter.AddKind(kind)
	return nil
}

// StartLogStreaming creates a LogStreamer and begins streaming logs for the
// given components. The LogStreamer uses the Debugger's shared filter and
// emitter, so log entries are filtered based on the currently watched
// resources. Call Stop to stop log streaming.
func (d *Debugger) StartLogStreaming(ctx context.Context, clientset kubernetes.Interface, namespace string, components ...Component) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logStreamer = NewLogStreamer(clientset, d.cache, d.emitter, d.filter, namespace, d.snapshotsDir, d.formatter, d.kindReg)
	d.logStreamer.Start(ctx, components...)
}

// Stop removes all event handlers, stops log streaming, and clears all state.
// After Stop returns, no further output will be emitted.
func (d *Debugger) Stop() {
	d.mu.Lock()
	ls := d.logStreamer
	d.logStreamer = nil
	watches := make(map[watchKey]watchHandle, len(d.watches))
	for k, v := range d.watches {
		watches[k] = v
	}
	d.watches = make(map[watchKey]watchHandle)
	d.relatedGroups = make(map[watchKey]*relatedEntry)
	d.mu.Unlock()

	if ls != nil {
		ls.Stop()
	}

	for _, handle := range watches {
		_ = handle.informer.RemoveEventHandler(handle.registration) // best-effort cleanup
	}

	d.state.Clear()

	d.mu.Lock()
	d.filter = newFilter(d.kindReg)
	d.mu.Unlock()
}

// RegisterKind records a bidirectional short↔full kind mapping on this
// Debugger's isolated registry.
func (d *Debugger) RegisterKind(shortKind, fullKind string) {
	d.kindReg.Register(shortKind, fullKind)
}

// KindRegistry returns this Debugger's kind registry for direct use.
func (d *Debugger) KindRegistry() *KindRegistry {
	return d.kindReg
}
