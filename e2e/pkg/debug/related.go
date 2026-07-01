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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ChildMatchStrategy determines how child resources are matched to a parent.
type ChildMatchStrategy int

const (
	// MatchByLabel matches children via a label whose value equals the parent name.
	MatchByLabel ChildMatchStrategy = iota

	// MatchBySpecField matches children via a spec field whose value equals the parent name.
	MatchBySpecField
)

// ChildRelation describes how a child GVK relates to its parent.
type ChildRelation struct {
	GVK           schema.GroupVersionKind
	Strategy      ChildMatchStrategy
	LabelKey      string // used when Strategy == MatchByLabel
	SpecFieldPath string // dot-separated JSON path, used when Strategy == MatchBySpecField
}

// RelationGraph maps a parent GVK to its child relations.
type RelationGraph map[schema.GroupVersionKind][]ChildRelation

// WatchRelated watches the parent object and all child resources defined in
// the relation graph. Label-based children are watched via WatchByLabel;
// spec-field-based children are watched via watchBySpecField.
//
// Multiple calls for the same parent increment a reference count on the
// related entry (and on each underlying watch). UnwatchRelated must be
// called the same number of times to fully tear down the watches.
func (d *Debugger) WatchRelated(obj client.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("object has no GVK set; call SetGroupVersionKind first")
	}

	parentName := obj.GetName()
	parentKey := watchKey{gvk: gvk, name: parentName}

	if err := d.Watch(obj); err != nil {
		return fmt.Errorf("watch parent %s/%s: %w", gvk.Kind, parentName, err)
	}

	d.mu.Lock()
	if entry, exists := d.relatedGroups[parentKey]; exists {
		entry.refCount++
		d.mu.Unlock()
		for _, childKey := range entry.childKeys {
			switch {
			case childKey.labelSelector != "":
				_ = d.WatchByLabel(childKey.gvk, childKey.labelSelector)
			case childKey.specFilter != "":
				parts := strings.SplitN(childKey.specFilter, "=", 2)
				if len(parts) == 2 {
					_ = d.watchBySpecField(childKey.gvk, parts[0], parts[1])
				}
			}
		}
		return nil
	}
	d.mu.Unlock()

	children := d.relationGraph[gvk]
	childKeys := make([]watchKey, 0, len(children))

	for _, child := range children {
		switch child.Strategy {
		case MatchByLabel:
			selector := child.LabelKey + "=" + parentName
			if err := d.WatchByLabel(child.GVK, selector); err != nil {
				d.mu.Lock()
				d.relatedGroups[parentKey] = &relatedEntry{childKeys: childKeys, refCount: 1}
				d.mu.Unlock()
				return fmt.Errorf("watch child %s by label: %w", child.GVK.Kind, err)
			}
			childKeys = append(childKeys, watchKey{gvk: child.GVK, labelSelector: selector})

		case MatchBySpecField:
			if err := d.watchBySpecField(child.GVK, child.SpecFieldPath, parentName); err != nil {
				d.mu.Lock()
				d.relatedGroups[parentKey] = &relatedEntry{childKeys: childKeys, refCount: 1}
				d.mu.Unlock()
				return fmt.Errorf("watch child %s by spec field: %w", child.GVK.Kind, err)
			}
			childKeys = append(childKeys, watchKey{gvk: child.GVK, specFilter: child.SpecFieldPath + "=" + parentName})
		}
	}

	d.mu.Lock()
	d.relatedGroups[parentKey] = &relatedEntry{childKeys: childKeys, refCount: 1}
	d.mu.Unlock()

	return nil
}

// UnwatchRelated decrements the reference count for the parent and all
// associated child watches. The informer handlers are only removed when
// the count drops to zero (i.e. every WatchRelated call has been balanced
// by an UnwatchRelated call).
func (d *Debugger) UnwatchRelated(obj client.Object) error {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		return fmt.Errorf("object has no GVK set; call SetGroupVersionKind first")
	}

	parentKey := watchKey{gvk: gvk, name: obj.GetName()}

	if err := d.unwatchByKey(parentKey); err != nil {
		return err
	}

	d.mu.Lock()
	entry := d.relatedGroups[parentKey]
	if entry == nil {
		d.mu.Unlock()
		return nil
	}
	entry.refCount--
	var childKeys []watchKey
	if entry.refCount <= 0 {
		childKeys = entry.childKeys
		delete(d.relatedGroups, parentKey)
	} else {
		childKeys = entry.childKeys
	}
	d.mu.Unlock()

	for _, childKey := range childKeys {
		if err := d.unwatchByKey(childKey); err != nil {
			return err
		}
	}

	return nil
}

// labelMatcher returns a function that checks whether the object's
// metadata.labels contains the given key=value pair parsed from
// labelSelector (format: "key=value"). An empty selector matches all objects.
func labelMatcher(labelSelector string) func(map[string]any) bool {
	if labelSelector == "" {
		return func(map[string]any) bool { return true }
	}
	parts := strings.SplitN(labelSelector, "=", 2)
	if len(parts) != 2 {
		return func(map[string]any) bool { return false }
	}
	key, value := parts[0], parts[1]
	return func(raw map[string]any) bool {
		meta, _ := raw["metadata"].(map[string]any)
		labels, _ := meta["labels"].(map[string]any)
		v, ok := labels[key].(string)
		if !ok {
			return false
		}
		return v == value
	}
}

// specFieldMatcher returns a function that checks whether the object's
// dot-separated field path equals expectedValue. The path is navigated
// through nested maps (e.g. "spec.replicatedVolumeName").
func specFieldMatcher(fieldPath, expectedValue string) func(map[string]any) bool {
	parts := strings.Split(fieldPath, ".")
	return func(raw map[string]any) bool {
		current := raw
		for i, part := range parts {
			if i == len(parts)-1 {
				val, _ := current[part].(string)
				return val == expectedValue
			}
			next, ok := current[part].(map[string]any)
			if !ok {
				return false
			}
			current = next
		}
		return false
	}
}
