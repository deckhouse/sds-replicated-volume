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
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	toolscache "k8s.io/client-go/tools/cache"
)

// eventHandler implements toolscache.ResourceEventHandler and processes
// watch events from a shared informer. For each event it computes diffs
// and conditions table changes and emits formatted output via the Emitter.
type eventHandler struct {
	emitter         Emitter
	formatter       Formatter
	tracker         *stateTracker
	kind            string // short kind name for display (e.g. "rv", "rvr")
	snapshotsDir    string
	renderCfg       RenderConfig
	kindReg         *KindRegistry
	objectMatchFunc func(raw map[string]any) bool
	matchFunc       func(kind, name string) bool
}

var _ toolscache.ResourceEventHandler = (*eventHandler)(nil)

// OnAdd is called by the informer when a new object appears (or is
// re-listed). The isInInitialList parameter indicates whether this is
// part of the initial list sync.
func (h *eventHandler) OnAdd(obj interface{}, _ bool) {
	h.handleEvent(obj, false)
}

// OnUpdate is called when an existing object is modified.
func (h *eventHandler) OnUpdate(_, newObj interface{}) {
	h.handleEvent(newObj, false)
}

// OnDelete is called when an object is deleted.
func (h *eventHandler) OnDelete(obj interface{}) {
	h.handleEvent(obj, true)
}

func (h *eventHandler) handleEvent(obj interface{}, deleted bool) {
	raw := toUnstructuredMap(obj)
	if raw == nil {
		return
	}

	meta, _ := raw["metadata"].(map[string]any)
	objName, _ := meta["name"].(string)
	if objName == "" {
		return
	}

	if h.objectMatchFunc != nil && !h.objectMatchFunc(raw) {
		return
	}

	if h.matchFunc != nil && !h.matchFunc(h.kind, objName) {
		return
	}

	snapPath := SaveSnapshot(h.snapshotsDir, h.kind, objName, MarshalYAML(raw))
	objLink := objName
	if snapPath != "" {
		objLink = OSC8Link(objName, snapPath)
	}

	newConds := ExtractConditions(raw)
	RemoveConditions(raw)
	newLines := PrettyLines(raw)

	key := stateKey{GVK: h.kind, Name: objName}

	if deleted {
		h.emitter.Emit(h.formatter.FormatDeleted(h.kind, objLink))
		h.tracker.Delete(key)
		return
	}

	prev := h.tracker.Get(key)
	h.tracker.Set(key, &objectState{Lines: newLines, Conditions: newConds})

	if prev == nil {
		condLines := ConditionsTableNew(newConds, h.renderCfg)
		block := h.formatter.FormatAdded(h.kind, objLink, condLines, newLines)
		h.emitter.EmitBlock(block)
	} else {
		diff := UnifiedDiff(prev.Lines, newLines)
		condsChanged := !ConditionsEqual(prev.Conditions, newConds)
		if len(diff) == 0 && !condsChanged {
			return
		}
		condLines := ConditionsTableDiff(prev.Conditions, newConds, h.renderCfg)
		block := h.formatter.FormatModified(h.kind, objLink, condLines, diff)
		h.emitter.EmitBlock(block)
	}
}

// toUnstructuredMap converts a runtime.Object (or *unstructured.Unstructured,
// or a tombstone wrapper) into a plain map[string]any. It uses the standard
// unstructured converter when possible, falling back to JSON round-trip.
func toUnstructuredMap(obj interface{}) map[string]any {
	if obj == nil {
		return nil
	}

	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
		if obj == nil {
			return nil
		}
	}

	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u.Object
	}

	if rObj, ok := obj.(runtime.Object); ok {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rObj)
		if err == nil {
			return m
		}
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
