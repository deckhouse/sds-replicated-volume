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

package nodeidset_test

import (
	"slices"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/nodeidset"
)

// ----------------------------------------------------------------------------
// Constructors
// ----------------------------------------------------------------------------

func TestOf(t *testing.T) {
	// Of(id) creates a singleton set containing only that ID.
	for _, id := range []uint8{0, 1, 15, 31} {
		s := nodeidset.Of(id)
		if !s.Contains(id) {
			t.Fatalf("Of(%d) does not contain %d", id, id)
		}
		if s.Len() != 1 {
			t.Fatalf("Of(%d) has len %d, want 1", id, s.Len())
		}
		if s.Min() != id {
			t.Fatalf("Of(%d).Min() = %d, want %d", id, s.Min(), id)
		}
		if s.Max() != id {
			t.Fatalf("Of(%d).Max() = %d, want %d", id, s.Max(), id)
		}
	}

	// Of(id) is equivalent to NodeIDSet(1 << id).
	for id := uint8(0); id < 32; id++ {
		got := nodeidset.Of(id)
		want := nodeidset.NodeIDSet(1 << id)
		if got != want {
			t.Fatalf("Of(%d) = %v, want %v", id, got, want)
		}
	}
}

// ----------------------------------------------------------------------------
// Basic operations
// ----------------------------------------------------------------------------

func TestNodeIDSet_AddContainsRemove(t *testing.T) {
	var s nodeidset.NodeIDSet

	// Empty set contains nothing.
	if s.Contains(0) {
		t.Fatal("expected empty set to not contain 0")
	}
	if s.Contains(31) {
		t.Fatal("expected empty set to not contain 31")
	}

	// Add and verify.
	s.Add(0)
	s.Add(15)
	s.Add(31)

	if !s.Contains(0) {
		t.Fatal("expected set to contain 0")
	}
	if !s.Contains(15) {
		t.Fatal("expected set to contain 15")
	}
	if !s.Contains(31) {
		t.Fatal("expected set to contain 31")
	}
	if s.Contains(1) {
		t.Fatal("expected set to not contain 1")
	}
	if s.Contains(30) {
		t.Fatal("expected set to not contain 30")
	}

	// Remove and verify.
	s.Remove(15)
	if s.Contains(15) {
		t.Fatal("expected set to not contain 15 after removal")
	}
	if !s.Contains(0) {
		t.Fatal("expected set to still contain 0")
	}
	if !s.Contains(31) {
		t.Fatal("expected set to still contain 31")
	}

	// Remove non-existent element is no-op.
	s.Remove(15)
	if s.Len() != 2 {
		t.Fatalf("expected len=2, got %d", s.Len())
	}
}

func TestNodeIDSet_IsEmpty(t *testing.T) {
	var s nodeidset.NodeIDSet

	if !s.IsEmpty() {
		t.Fatal("expected empty set")
	}

	s.Add(5)
	if s.IsEmpty() {
		t.Fatal("expected non-empty set")
	}

	s.Remove(5)
	if !s.IsEmpty() {
		t.Fatal("expected empty set after removal")
	}
}

func TestNodeIDSet_Len(t *testing.T) {
	var s nodeidset.NodeIDSet

	if s.Len() != 0 {
		t.Fatalf("expected len=0, got %d", s.Len())
	}

	s.Add(0)
	if s.Len() != 1 {
		t.Fatalf("expected len=1, got %d", s.Len())
	}

	s.Add(31)
	if s.Len() != 2 {
		t.Fatalf("expected len=2, got %d", s.Len())
	}

	// Add duplicate -> no change.
	s.Add(0)
	if s.Len() != 2 {
		t.Fatalf("expected len=2 after duplicate add, got %d", s.Len())
	}

	// Full set.
	for i := uint8(0); i < 32; i++ {
		s.Add(i)
	}
	if s.Len() != 32 {
		t.Fatalf("expected len=32, got %d", s.Len())
	}
}

// ----------------------------------------------------------------------------
// Min / Max
// ----------------------------------------------------------------------------

func TestNodeIDSet_Min(t *testing.T) {
	var s nodeidset.NodeIDSet

	s.Add(5)
	s.Add(10)
	s.Add(3)
	if s.Min() != 3 {
		t.Fatalf("expected min=3, got %d", s.Min())
	}

	s.Add(0)
	if s.Min() != 0 {
		t.Fatalf("expected min=0, got %d", s.Min())
	}

	// Only element 31.
	s = 0
	s.Add(31)
	if s.Min() != 31 {
		t.Fatalf("expected min=31, got %d", s.Min())
	}
}

func TestNodeIDSet_Max(t *testing.T) {
	var s nodeidset.NodeIDSet

	s.Add(5)
	s.Add(10)
	s.Add(3)
	if s.Max() != 10 {
		t.Fatalf("expected max=10, got %d", s.Max())
	}

	s.Add(31)
	if s.Max() != 31 {
		t.Fatalf("expected max=31, got %d", s.Max())
	}

	// Only element 0.
	s = 0
	s.Add(0)
	if s.Max() != 0 {
		t.Fatalf("expected max=0, got %d", s.Max())
	}
}

func TestNodeIDSet_MinMaxOnEmpty_UndefinedBehavior(t *testing.T) {
	// Note: Min/Max on empty set is documented as "Panics if the set is empty",
	// but the implementation returns 32 (outside valid 0-31 range) via bits.TrailingZeros32/LeadingZeros32.
	// This test documents actual behavior; callers must check IsEmpty() before calling Min/Max.
	var s nodeidset.NodeIDSet

	// bits.TrailingZeros32(0) == 32
	if s.Min() != 32 {
		t.Fatalf("expected Min()=32 on empty set (undefined behavior), got %d", s.Min())
	}

	// 31 - bits.LeadingZeros32(0) == 31 - 32 == -1 -> uint8 wraps to 255
	// Actually: bits.LeadingZeros32(0) returns 32, so 31-32 = -1, uint8(-1) = 255
	if s.Max() != 255 {
		t.Fatalf("expected Max()=255 on empty set (undefined behavior), got %d", s.Max())
	}
}

// ----------------------------------------------------------------------------
// MinMissing
// ----------------------------------------------------------------------------

func TestNodeIDSet_MinMissing(t *testing.T) {
	var s nodeidset.NodeIDSet

	// Empty set -> 0 is missing.
	id, ok := s.MinMissing()
	if !ok || id != 0 {
		t.Fatalf("expected (0, true), got (%d, %v)", id, ok)
	}

	// {0} -> 1 is missing.
	s.Add(0)
	id, ok = s.MinMissing()
	if !ok || id != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", id, ok)
	}

	// {0, 1, 2} -> 3 is missing.
	s.Add(1)
	s.Add(2)
	id, ok = s.MinMissing()
	if !ok || id != 3 {
		t.Fatalf("expected (3, true), got (%d, %v)", id, ok)
	}

	// {0, 2} -> 1 is missing.
	s.Remove(1)
	id, ok = s.MinMissing()
	if !ok || id != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", id, ok)
	}

	// Full set -> nothing missing.
	s = nodeidset.NodeIDSet(0xFFFFFFFF)
	id, ok = s.MinMissing()
	if ok {
		t.Fatalf("expected (_, false) for full set, got (%d, %v)", id, ok)
	}
}

// ----------------------------------------------------------------------------
// Iteration
// ----------------------------------------------------------------------------

func TestNodeIDSet_All(t *testing.T) {
	var s nodeidset.NodeIDSet
	s.Add(3)
	s.Add(0)
	s.Add(7)
	s.Add(31)

	var got []uint8
	for id := range s.All() {
		got = append(got, id)
	}

	want := []uint8{0, 3, 7, 31}
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNodeIDSet_All_Empty(t *testing.T) {
	var s nodeidset.NodeIDSet

	var count int
	for range s.All() {
		count++
	}

	if count != 0 {
		t.Fatalf("expected 0 iterations, got %d", count)
	}
}

func TestNodeIDSet_All_EarlyBreak(t *testing.T) {
	var s nodeidset.NodeIDSet
	s.Add(1)
	s.Add(5)
	s.Add(10)

	var got []uint8
	for id := range s.All() {
		got = append(got, id)
		if id == 5 {
			break
		}
	}

	want := []uint8{1, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNodeIDSet_AppendTo(t *testing.T) {
	var s nodeidset.NodeIDSet
	s.Add(2)
	s.Add(5)
	s.Add(8)

	// Append to nil.
	got := s.AppendTo(nil)
	want := []uint8{2, 5, 8}
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Append to existing slice.
	existing := []uint8{100, 200}
	got = s.AppendTo(existing)
	want = []uint8{100, 200, 2, 5, 8}
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Empty set appends nothing.
	var empty nodeidset.NodeIDSet
	got = empty.AppendTo([]uint8{1})
	want = []uint8{1}
	if !slices.Equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

// ----------------------------------------------------------------------------
// String
// ----------------------------------------------------------------------------

func TestNodeIDSet_String(t *testing.T) {
	tests := []struct {
		name string
		set  nodeidset.NodeIDSet
		want string
	}{
		{"empty", 0, ""},
		{"single_0", 1 << 0, "0"},
		{"single_31", 1 << 31, "31"},
		{"two_elements", (1 << 0) | (1 << 5), "0, 5"},
		{"three_elements", (1 << 3) | (1 << 7) | (1 << 15), "3, 7, 15"},
		{"consecutive", (1 << 0) | (1 << 1) | (1 << 2), "0, 1, 2"},
		{"mixed", (1 << 0) | (1 << 10) | (1 << 20) | (1 << 31), "0, 10, 20, 31"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.set.String()
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Set algebra
// ----------------------------------------------------------------------------

func TestNodeIDSet_Union(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 1) | (1 << 2))

	got := a.Union(b)
	want := nodeidset.NodeIDSet((1 << 0) | (1 << 1) | (1 << 2))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Union with empty.
	if a.Union(0) != a {
		t.Fatal("union with empty should return self")
	}
}

func TestNodeIDSet_Intersect(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2) | (1 << 3))
	b := nodeidset.NodeIDSet((1 << 1) | (1 << 2) | (1 << 3))

	got := a.Intersect(b)
	want := nodeidset.NodeIDSet((1 << 2) | (1 << 3))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Intersect with empty.
	if a.Intersect(0) != 0 {
		t.Fatal("intersect with empty should return empty")
	}

	// Disjoint sets.
	c := nodeidset.Of(5)
	if a.Intersect(c) != 0 {
		t.Fatal("intersect of disjoint sets should be empty")
	}
}

func TestNodeIDSet_Difference(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2) | (1 << 3))
	b := nodeidset.NodeIDSet((1 << 2) | (1 << 5))

	got := a.Difference(b)
	want := nodeidset.NodeIDSet((1 << 0) | (1 << 3))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Difference with empty.
	if a.Difference(0) != a {
		t.Fatal("difference with empty should return self")
	}

	// Difference with self.
	if a.Difference(a) != 0 {
		t.Fatal("difference with self should be empty")
	}
}

func TestNodeIDSet_SymDiff(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 1) | (1 << 2))

	got := a.SymDiff(b)
	want := nodeidset.NodeIDSet((1 << 0) | (1 << 1))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// SymDiff with self is empty.
	if a.SymDiff(a) != 0 {
		t.Fatal("symdiff with self should be empty")
	}

	// SymDiff with empty is self.
	if a.SymDiff(0) != a {
		t.Fatal("symdiff with empty should return self")
	}
}

// ----------------------------------------------------------------------------
// Set predicates
// ----------------------------------------------------------------------------

func TestNodeIDSet_IsSubsetOf(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 0) | (1 << 2) | (1 << 5))

	if !a.IsSubsetOf(b) {
		t.Fatal("expected a ⊆ b")
	}
	if b.IsSubsetOf(a) {
		t.Fatal("expected b ⊄ a")
	}

	// Set is subset of itself.
	if !a.IsSubsetOf(a) {
		t.Fatal("expected a ⊆ a")
	}

	// Empty is subset of everything.
	var empty nodeidset.NodeIDSet
	if !empty.IsSubsetOf(a) {
		t.Fatal("expected ∅ ⊆ a")
	}
	if !empty.IsSubsetOf(empty) {
		t.Fatal("expected ∅ ⊆ ∅")
	}
}

func TestNodeIDSet_Equals(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	c := nodeidset.NodeIDSet((1 << 0) | (1 << 3))

	if !a.Equals(b) {
		t.Fatal("expected a == b")
	}
	if a.Equals(c) {
		t.Fatal("expected a ≠ c")
	}

	var empty nodeidset.NodeIDSet
	if !empty.Equals(0) {
		t.Fatal("expected empty == 0")
	}
}

func TestNodeIDSet_Overlaps(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 2) | (1 << 5))
	c := nodeidset.NodeIDSet((1 << 3) | (1 << 4))

	if !a.Overlaps(b) {
		t.Fatal("expected a and b to overlap")
	}
	if a.Overlaps(c) {
		t.Fatal("expected a and c to not overlap")
	}

	// Empty overlaps nothing.
	var empty nodeidset.NodeIDSet
	if empty.Overlaps(a) {
		t.Fatal("expected empty to not overlap with a")
	}
	if a.Overlaps(empty) {
		t.Fatal("expected a to not overlap with empty")
	}
}

func TestNodeIDSet_IsDisjointWith(t *testing.T) {
	a := nodeidset.NodeIDSet((1 << 0) | (1 << 2))
	b := nodeidset.NodeIDSet((1 << 2) | (1 << 5))
	c := nodeidset.NodeIDSet((1 << 3) | (1 << 4))

	if a.IsDisjointWith(b) {
		t.Fatal("expected a and b to not be disjoint")
	}
	if !a.IsDisjointWith(c) {
		t.Fatal("expected a and c to be disjoint")
	}

	// Empty is disjoint with everything.
	var empty nodeidset.NodeIDSet
	if !empty.IsDisjointWith(a) {
		t.Fatal("expected empty to be disjoint with a")
	}
	if !empty.IsDisjointWith(empty) {
		t.Fatal("expected empty to be disjoint with empty")
	}
}

// ----------------------------------------------------------------------------
// Generic helpers
// ----------------------------------------------------------------------------

type testNode struct {
	id     uint8
	active bool
}

func (n testNode) NodeID() uint8 { return n.id }

func TestFromAll(t *testing.T) {
	nodes := []testNode{
		{id: 0},
		{id: 5},
		{id: 10},
	}

	got := nodeidset.FromAll(nodes)
	want := nodeidset.NodeIDSet((1 << 0) | (1 << 5) | (1 << 10))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Empty slice.
	got = nodeidset.FromAll([]testNode{})
	if got != 0 {
		t.Fatalf("expected empty set, got %v", got)
	}

	// Nil slice.
	got = nodeidset.FromAll[testNode](nil)
	if got != 0 {
		t.Fatalf("expected empty set for nil slice, got %v", got)
	}
}

func TestFromWhere(t *testing.T) {
	nodes := []testNode{
		{id: 0, active: true},
		{id: 5, active: false},
		{id: 10, active: true},
		{id: 15, active: false},
	}

	got := nodeidset.FromWhere(nodes, func(n testNode) bool { return n.active })
	want := nodeidset.NodeIDSet((1 << 0) | (1 << 10))

	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// No matches.
	got = nodeidset.FromWhere(nodes, func(_ testNode) bool { return false })
	if got != 0 {
		t.Fatalf("expected empty set, got %v", got)
	}

	// All match.
	got = nodeidset.FromWhere(nodes, func(_ testNode) bool { return true })
	want = nodeidset.NodeIDSet((1 << 0) | (1 << 5) | (1 << 10) | (1 << 15))
	if got != want {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Empty slice.
	got = nodeidset.FromWhere([]testNode{}, func(_ testNode) bool { return true })
	if got != 0 {
		t.Fatalf("expected empty set, got %v", got)
	}
}

// ----------------------------------------------------------------------------
// Edge cases
// ----------------------------------------------------------------------------

func TestNodeIDSet_FullSet(t *testing.T) {
	full := nodeidset.NodeIDSet(0xFFFFFFFF)

	if full.Len() != 32 {
		t.Fatalf("expected len=32, got %d", full.Len())
	}
	if full.Min() != 0 {
		t.Fatalf("expected min=0, got %d", full.Min())
	}
	if full.Max() != 31 {
		t.Fatalf("expected max=31, got %d", full.Max())
	}
	if full.IsEmpty() {
		t.Fatal("expected non-empty")
	}

	for i := uint8(0); i < 32; i++ {
		if !full.Contains(i) {
			t.Fatalf("expected full set to contain %d", i)
		}
	}

	// MinMissing returns false.
	_, ok := full.MinMissing()
	if ok {
		t.Fatal("expected MinMissing to return false for full set")
	}
}

func TestNodeIDSet_BoundaryValues(t *testing.T) {
	var s nodeidset.NodeIDSet

	// Test boundary IDs: 0 and 31.
	s.Add(0)
	s.Add(31)

	if !s.Contains(0) || !s.Contains(31) {
		t.Fatal("expected set to contain boundary values")
	}
	if s.Len() != 2 {
		t.Fatalf("expected len=2, got %d", s.Len())
	}
	if s.Min() != 0 {
		t.Fatalf("expected min=0, got %d", s.Min())
	}
	if s.Max() != 31 {
		t.Fatalf("expected max=31, got %d", s.Max())
	}

	s.Remove(0)
	s.Remove(31)
	if !s.IsEmpty() {
		t.Fatal("expected empty set after removing boundary values")
	}
}
