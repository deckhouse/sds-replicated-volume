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

package maps

import (
	"maps"
	"slices"
	"testing"
)

func TestIntersect(t *testing.T) {
	tests := []struct {
		name      string
		left      map[string]int
		right     map[string]int
		wantLeft  map[string]int
		wantBoth  map[string]ValuePair[int, int]
		wantRight map[string]int
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "partial overlap different values",
			left:      map[string]int{"a": 1, "b": 2},
			right:     map[string]int{"b": 20, "c": 3},
			wantLeft:  map[string]int{"a": 1},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 2, Right: 20}},
			wantRight: map[string]int{"c": 3},
		},
		{
			name:      "full overlap same values",
			left:      map[string]int{"a": 1},
			right:     map[string]int{"a": 1},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"a": {Left: 1, Right: 1}},
			wantRight: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := Intersect(tt.left, tt.right)

			if !equalMaps(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", gotLeft, tt.wantLeft)
			}
			if !equalValuePairMaps(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", gotBoth, tt.wantBoth)
			}
			if !equalMaps(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", gotRight, tt.wantRight)
			}
		})
	}
}

func TestIntersectKeys(t *testing.T) {
	tests := []struct {
		name      string
		left      map[string]int
		right     map[string]string
		wantLeft  []string
		wantBoth  []string
		wantRight []string
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "partial overlap ignores values",
			left:      map[string]int{"a": 1, "b": 2},
			right:     map[string]string{"b": "twenty", "c": "three"},
			wantLeft:  []string{"a"},
			wantBoth:  []string{"b"},
			wantRight: []string{"c"},
		},
		{
			name:      "full overlap different value types",
			left:      map[string]int{"x": 100},
			right:     map[string]string{"x": "hundred"},
			wantLeft:  nil,
			wantBoth:  []string{"x"},
			wantRight: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := IntersectKeys(tt.left, tt.right)

			if !equalKeySet(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", mapKeys(gotLeft), tt.wantLeft)
			}
			if !equalKeySet(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", mapKeys(gotBoth), tt.wantBoth)
			}
			if !equalKeySet(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", mapKeys(gotRight), tt.wantRight)
			}
		})
	}
}

func TestIntersectIters(t *testing.T) {
	tests := []struct {
		name      string
		left      []string
		right     []string
		wantLeft  []string
		wantBoth  []string
		wantRight []string
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "left empty",
			left:      nil,
			right:     []string{"a", "b"},
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: []string{"a", "b"},
		},
		{
			name:      "right empty",
			left:      []string{"a", "b"},
			right:     nil,
			wantLeft:  []string{"a", "b"},
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "no overlap",
			left:      []string{"a", "b"},
			right:     []string{"c", "d"},
			wantLeft:  []string{"a", "b"},
			wantBoth:  nil,
			wantRight: []string{"c", "d"},
		},
		{
			name:      "full overlap",
			left:      []string{"a", "b"},
			right:     []string{"a", "b"},
			wantLeft:  nil,
			wantBoth:  []string{"a", "b"},
			wantRight: nil,
		},
		{
			name:      "partial overlap",
			left:      []string{"a", "b", "c"},
			right:     []string{"b", "c", "d"},
			wantLeft:  []string{"a"},
			wantBoth:  []string{"b", "c"},
			wantRight: []string{"d"},
		},
		{
			name:      "left subset of right",
			left:      []string{"b"},
			right:     []string{"a", "b", "c"},
			wantLeft:  nil,
			wantBoth:  []string{"b"},
			wantRight: []string{"a", "c"},
		},
		{
			name:      "right subset of left",
			left:      []string{"a", "b", "c"},
			right:     []string{"b"},
			wantLeft:  []string{"a", "c"},
			wantBoth:  []string{"b"},
			wantRight: nil,
		},
		{
			name:      "single element each no overlap",
			left:      []string{"x"},
			right:     []string{"y"},
			wantLeft:  []string{"x"},
			wantBoth:  nil,
			wantRight: []string{"y"},
		},
		{
			name:      "single element each with overlap",
			left:      []string{"x"},
			right:     []string{"x"},
			wantLeft:  nil,
			wantBoth:  []string{"x"},
			wantRight: nil,
		},
		{
			name:      "duplicates in left iterator",
			left:      []string{"a", "a", "b"},
			right:     []string{"b", "c"},
			wantLeft:  []string{"a"},
			wantBoth:  []string{"b"},
			wantRight: []string{"c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := IntersectIters(slices.Values(tt.left), slices.Values(tt.right))

			if !equalKeySet(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", mapKeys(gotLeft), tt.wantLeft)
			}
			if !equalKeySet(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", mapKeys(gotBoth), tt.wantBoth)
			}
			if !equalKeySet(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", mapKeys(gotRight), tt.wantRight)
			}
		})
	}
}

func TestIntersectIters2(t *testing.T) {
	tests := []struct {
		name      string
		left      map[string]int
		right     map[string]int
		wantLeft  map[string]int
		wantBoth  map[string]ValuePair[int, int]
		wantRight map[string]int
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "left empty",
			left:      nil,
			right:     map[string]int{"a": 1, "b": 2},
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: map[string]int{"a": 1, "b": 2},
		},
		{
			name:      "right empty",
			left:      map[string]int{"a": 1, "b": 2},
			right:     nil,
			wantLeft:  map[string]int{"a": 1, "b": 2},
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "no overlap",
			left:      map[string]int{"a": 1, "b": 2},
			right:     map[string]int{"c": 3, "d": 4},
			wantLeft:  map[string]int{"a": 1, "b": 2},
			wantBoth:  nil,
			wantRight: map[string]int{"c": 3, "d": 4},
		},
		{
			name:      "full overlap same values",
			left:      map[string]int{"a": 1, "b": 2},
			right:     map[string]int{"a": 1, "b": 2},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"a": {Left: 1, Right: 1}, "b": {Left: 2, Right: 2}},
			wantRight: nil,
		},
		{
			name:      "full overlap different values",
			left:      map[string]int{"a": 1, "b": 2},
			right:     map[string]int{"a": 10, "b": 20},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"a": {Left: 1, Right: 10}, "b": {Left: 2, Right: 20}},
			wantRight: nil,
		},
		{
			name:      "partial overlap",
			left:      map[string]int{"a": 1, "b": 2, "c": 3},
			right:     map[string]int{"b": 20, "c": 30, "d": 40},
			wantLeft:  map[string]int{"a": 1},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 2, Right: 20}, "c": {Left: 3, Right: 30}},
			wantRight: map[string]int{"d": 40},
		},
		{
			name:      "left subset of right",
			left:      map[string]int{"b": 2},
			right:     map[string]int{"a": 1, "b": 20, "c": 3},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 2, Right: 20}},
			wantRight: map[string]int{"a": 1, "c": 3},
		},
		{
			name:      "right subset of left",
			left:      map[string]int{"a": 1, "b": 2, "c": 3},
			right:     map[string]int{"b": 20},
			wantLeft:  map[string]int{"a": 1, "c": 3},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 2, Right: 20}},
			wantRight: nil,
		},
		{
			name:      "single element each no overlap",
			left:      map[string]int{"x": 100},
			right:     map[string]int{"y": 200},
			wantLeft:  map[string]int{"x": 100},
			wantBoth:  nil,
			wantRight: map[string]int{"y": 200},
		},
		{
			name:      "single element each with overlap",
			left:      map[string]int{"x": 100},
			right:     map[string]int{"x": 200},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"x": {Left: 100, Right: 200}},
			wantRight: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := IntersectIters2(maps.All(tt.left), maps.All(tt.right))

			if !equalMaps(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", gotLeft, tt.wantLeft)
			}
			if !equalValuePairMaps(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", gotBoth, tt.wantBoth)
			}
			if !equalMaps(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", gotRight, tt.wantRight)
			}
		})
	}
}

func TestIntersectItersValueFunc(t *testing.T) {
	tests := []struct {
		name      string
		left      []string
		right     []string
		wantLeft  map[string]int
		wantBoth  map[string]ValuePair[int, int]
		wantRight map[string]int
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "left empty",
			left:      nil,
			right:     []string{"a", "b"},
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: map[string]int{"a": 1, "b": 1},
		},
		{
			name:      "right empty",
			left:      []string{"a", "b"},
			right:     nil,
			wantLeft:  map[string]int{"a": 1, "b": 1},
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "no overlap",
			left:      []string{"a", "b"},
			right:     []string{"c", "d"},
			wantLeft:  map[string]int{"a": 1, "b": 1},
			wantBoth:  nil,
			wantRight: map[string]int{"c": 1, "d": 1},
		},
		{
			name:      "full overlap",
			left:      []string{"a", "b"},
			right:     []string{"a", "b"},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"a": {Left: 1, Right: 1}, "b": {Left: 1, Right: 1}},
			wantRight: nil,
		},
		{
			name:      "partial overlap",
			left:      []string{"a", "b", "c"},
			right:     []string{"b", "c", "d"},
			wantLeft:  map[string]int{"a": 1},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 1, Right: 1}, "c": {Left: 1, Right: 1}},
			wantRight: map[string]int{"d": 1},
		},
		{
			name:      "left subset of right",
			left:      []string{"b"},
			right:     []string{"a", "b", "c"},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 1, Right: 1}},
			wantRight: map[string]int{"a": 1, "c": 1},
		},
		{
			name:      "right subset of left",
			left:      []string{"a", "b", "c"},
			right:     []string{"b"},
			wantLeft:  map[string]int{"a": 1, "c": 1},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 1, Right: 1}},
			wantRight: nil,
		},
		{
			name:      "single element each no overlap",
			left:      []string{"x"},
			right:     []string{"y"},
			wantLeft:  map[string]int{"x": 1},
			wantBoth:  nil,
			wantRight: map[string]int{"y": 1},
		},
		{
			name:      "single element each with overlap",
			left:      []string{"x"},
			right:     []string{"x"},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[int, int]{"x": {Left: 1, Right: 1}},
			wantRight: nil,
		},
		{
			name:      "duplicates in left iterator",
			left:      []string{"a", "a", "b"},
			right:     []string{"b", "c"},
			wantLeft:  map[string]int{"a": 1},
			wantBoth:  map[string]ValuePair[int, int]{"b": {Left: 1, Right: 1}},
			wantRight: map[string]int{"c": 1},
		},
	}

	// Value functions that return length of key
	leftValueFunc := func(k string) int { return len(k) }
	rightValueFunc := func(k string) int { return len(k) }

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := IntersectItersValueFunc(
				slices.Values(tt.left),
				leftValueFunc,
				slices.Values(tt.right),
				rightValueFunc,
			)

			if !equalMaps(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", gotLeft, tt.wantLeft)
			}
			if !equalValuePairMaps(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", gotBoth, tt.wantBoth)
			}
			if !equalMaps(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", gotRight, tt.wantRight)
			}
		})
	}
}

func TestIntersectItersValueFunc_DifferentValueFuncs(t *testing.T) {
	// Test with different value functions for left and right
	left := []string{"a", "bb", "ccc"}
	right := []string{"bb", "ccc", "dddd"}

	// Left returns length, right returns length * 10
	leftValueFunc := func(k string) int { return len(k) }
	rightValueFunc := func(k string) int { return len(k) * 10 }

	gotLeft, gotBoth, gotRight := IntersectItersValueFunc(
		slices.Values(left),
		leftValueFunc,
		slices.Values(right),
		rightValueFunc,
	)

	wantLeft := map[string]int{"a": 1}
	wantBoth := map[string]ValuePair[int, int]{
		"bb":  {Left: 2, Right: 20},
		"ccc": {Left: 3, Right: 30},
	}
	wantRight := map[string]int{"dddd": 40}

	if !equalMaps(gotLeft, wantLeft) {
		t.Errorf("onlyLeft = %v, want %v", gotLeft, wantLeft)
	}
	if !equalValuePairMaps(gotBoth, wantBoth) {
		t.Errorf("both = %v, want %v", gotBoth, wantBoth)
	}
	if !equalMaps(gotRight, wantRight) {
		t.Errorf("onlyRight = %v, want %v", gotRight, wantRight)
	}
}

type item struct {
	id   string
	data int
}

func TestIntersectItersKeyFunc(t *testing.T) {
	tests := []struct {
		name      string
		left      []item
		right     []item
		wantLeft  map[string]item
		wantBoth  map[string]ValuePair[item, item]
		wantRight map[string]item
	}{
		{
			name:      "both empty",
			left:      nil,
			right:     nil,
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "left empty",
			left:      nil,
			right:     []item{{id: "a", data: 1}, {id: "b", data: 2}},
			wantLeft:  nil,
			wantBoth:  nil,
			wantRight: map[string]item{"a": {id: "a", data: 1}, "b": {id: "b", data: 2}},
		},
		{
			name:      "right empty",
			left:      []item{{id: "a", data: 1}, {id: "b", data: 2}},
			right:     nil,
			wantLeft:  map[string]item{"a": {id: "a", data: 1}, "b": {id: "b", data: 2}},
			wantBoth:  nil,
			wantRight: nil,
		},
		{
			name:      "no overlap",
			left:      []item{{id: "a", data: 1}},
			right:     []item{{id: "b", data: 2}},
			wantLeft:  map[string]item{"a": {id: "a", data: 1}},
			wantBoth:  nil,
			wantRight: map[string]item{"b": {id: "b", data: 2}},
		},
		{
			name:      "full overlap same data",
			left:      []item{{id: "a", data: 1}},
			right:     []item{{id: "a", data: 1}},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[item, item]{"a": {Left: item{id: "a", data: 1}, Right: item{id: "a", data: 1}}},
			wantRight: nil,
		},
		{
			name:      "full overlap different data",
			left:      []item{{id: "a", data: 1}},
			right:     []item{{id: "a", data: 100}},
			wantLeft:  nil,
			wantBoth:  map[string]ValuePair[item, item]{"a": {Left: item{id: "a", data: 1}, Right: item{id: "a", data: 100}}},
			wantRight: nil,
		},
		{
			name:      "partial overlap",
			left:      []item{{id: "a", data: 1}, {id: "b", data: 2}},
			right:     []item{{id: "b", data: 20}, {id: "c", data: 3}},
			wantLeft:  map[string]item{"a": {id: "a", data: 1}},
			wantBoth:  map[string]ValuePair[item, item]{"b": {Left: item{id: "b", data: 2}, Right: item{id: "b", data: 20}}},
			wantRight: map[string]item{"c": {id: "c", data: 3}},
		},
	}

	keyFunc := func(i item) string { return i.id }

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLeft, gotBoth, gotRight := IntersectItersKeyFunc(
				slices.Values(tt.left),
				keyFunc,
				slices.Values(tt.right),
				keyFunc,
			)

			if !equalItemMaps(gotLeft, tt.wantLeft) {
				t.Errorf("onlyLeft = %v, want %v", gotLeft, tt.wantLeft)
			}
			if !equalItemPairMaps(gotBoth, tt.wantBoth) {
				t.Errorf("both = %v, want %v", gotBoth, tt.wantBoth)
			}
			if !equalItemMaps(gotRight, tt.wantRight) {
				t.Errorf("onlyRight = %v, want %v", gotRight, tt.wantRight)
			}
		})
	}
}

// Helper functions for tests

func equalItemMaps(got, want map[string]item) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	for k, wv := range want {
		if gv, ok := got[k]; !ok || gv != wv {
			return false
		}
	}
	return true
}

func equalItemPairMaps(got, want map[string]ValuePair[item, item]) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	for k, wv := range want {
		if gv, ok := got[k]; !ok || gv.Left != wv.Left || gv.Right != wv.Right {
			return false
		}
	}
	return true
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	if m == nil {
		return nil
	}
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func equalKeySet[K comparable](got map[K]struct{}, want []K) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			return false
		}
	}
	return true
}

func equalMaps[K comparable, V comparable](got, want map[K]V) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	for k, wv := range want {
		if gv, ok := got[k]; !ok || gv != wv {
			return false
		}
	}
	return true
}

func equalValuePairMaps[K comparable, VL comparable, VR comparable](got, want map[K]ValuePair[VL, VR]) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	if len(got) != len(want) {
		return false
	}
	for k, wv := range want {
		if gv, ok := got[k]; !ok || gv.Left != wv.Left || gv.Right != wv.Right {
			return false
		}
	}
	return true
}
