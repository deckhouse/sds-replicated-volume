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

// Condition represents a single entry from .status.conditions with the
// parent object's metadata.generation for observedGeneration comparison.
type Condition struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	LastTransitionTime string // raw ISO timestamp, "" if absent
	ObservedGeneration int64  // -1 means field absent
	Generation         int64  // object's metadata.generation (copied from parent)
}

// ExtractConditions reads .status.conditions from a JSON-decoded Kubernetes
// object. Each condition gets the object's metadata.generation so renderers
// can compare it with the per-condition observedGeneration.
func ExtractConditions(obj map[string]any) []Condition {
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		return nil
	}
	raw, _ := status["conditions"].([]any)
	if len(raw) == 0 {
		return nil
	}

	var gen int64
	if meta, _ := obj["metadata"].(map[string]any); meta != nil {
		if g, ok := meta["generation"].(float64); ok {
			gen = int64(g)
		}
	}

	conds := make([]Condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		og := int64(-1)
		if v, ok := m["observedGeneration"].(float64); ok {
			og = int64(v)
		}
		conds = append(conds, Condition{
			Type:               strVal(m, "type"),
			Status:             strVal(m, "status"),
			Reason:             strVal(m, "reason"),
			Message:            strVal(m, "message"),
			LastTransitionTime: strVal(m, "lastTransitionTime"),
			ObservedGeneration: og,
			Generation:         gen,
		})
	}
	return conds
}

// RemoveConditions removes .status.conditions from the object so they don't
// appear in the unified YAML diff (they're rendered separately as a table).
func RemoveConditions(obj map[string]any) {
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		return
	}
	delete(status, "conditions")
}

// CondChanged reports whether any visible field of a condition changed.
func CondChanged(old, cur Condition) bool {
	return old.Status != cur.Status ||
		old.Reason != cur.Reason ||
		old.Message != cur.Message ||
		old.LastTransitionTime != cur.LastTransitionTime ||
		old.ObservedGeneration != cur.ObservedGeneration ||
		old.Generation != cur.Generation
}

// ConditionsEqual reports whether two condition slices are equivalent
// (same types with identical field values, regardless of order).
func ConditionsEqual(a, b []Condition) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]Condition, len(a))
	for _, c := range a {
		am[c.Type] = c
	}
	for _, c := range b {
		old, ok := am[c.Type]
		if !ok {
			return false
		}
		if CondChanged(old, c) {
			return false
		}
	}
	return true
}
