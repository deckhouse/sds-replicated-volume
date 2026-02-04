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

package nodeidset

import (
	"iter"
	"math/bits"
)

// nodeIDStr is a lookup table for node ID string representations (0-31).
var nodeIDStr = [32]string{
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
	"20", "21", "22", "23", "24", "25", "26", "27", "28", "29",
	"30", "31",
}

// NodeIDSet is a bitset for node IDs (0-31).
type NodeIDSet uint32

// Contains reports whether the set contains id.
func (s NodeIDSet) Contains(id uint8) bool { return s&(1<<id) != 0 }

// Add adds id to the set.
func (s *NodeIDSet) Add(id uint8) { *s |= 1 << id }

// Remove removes id from the set.
func (s *NodeIDSet) Remove(id uint8) { *s &^= 1 << id }

// IsEmpty reports whether the set is empty.
func (s NodeIDSet) IsEmpty() bool { return s == 0 }

// Len returns the number of node IDs in the set.
func (s NodeIDSet) Len() int { return bits.OnesCount32(uint32(s)) }

// Min returns the smallest node ID in the set.
// Panics if the set is empty.
func (s NodeIDSet) Min() uint8 { return uint8(bits.TrailingZeros32(uint32(s))) }

// Max returns the largest node ID in the set.
// Panics if the set is empty.
func (s NodeIDSet) Max() uint8 { return uint8(31 - bits.LeadingZeros32(uint32(s))) }

// MinMissing returns the smallest node ID that is not in the set.
// Returns (id, true) if found, or (0, false) if all 32 IDs are present.
func (s NodeIDSet) MinMissing() (uint8, bool) {
	inv := ^uint32(s)
	if inv == 0 {
		return 0, false
	}
	return uint8(bits.TrailingZeros32(inv)), true
}

// All returns an iterator (iter.Seq) over node IDs in the set (ascending).
// Usage: for id := range s.All() { ... }
func (s NodeIDSet) All() iter.Seq[uint8] {
	return func(yield func(uint8) bool) {
		x := uint32(s)
		for x != 0 {
			i := bits.TrailingZeros32(x)
			if !yield(uint8(i)) {
				return
			}
			x &^= 1 << uint(i)
		}
	}
}

// AppendTo appends all set node IDs to dst (ascending) and returns dst.
func (s NodeIDSet) AppendTo(dst []uint8) []uint8 {
	for id := range s.All() {
		dst = append(dst, id)
	}
	return dst
}

// String returns a comma-separated list of node IDs (e.g., "0, 3, 7").
// Uses a fixed-size stack buffer; only one allocation for the final string.
func (s NodeIDSet) String() string {
	if s == 0 {
		return ""
	}
	// Max capacity: 10 one-digit (0-9) + 22 two-digit (10-31) + 31 separators (", ") = 116 bytes.
	var buf [116]byte
	b := buf[:0]
	x := uint32(s)
	for x != 0 {
		if len(b) > 0 {
			b = append(b, ',', ' ')
		}
		i := bits.TrailingZeros32(x)
		b = append(b, nodeIDStr[i]...)
		x &^= 1 << uint(i)
	}
	return string(b)
}

// ---- Set algebra

// Union returns s ∪ t.
func (s NodeIDSet) Union(t NodeIDSet) NodeIDSet { return s | t }

// Intersect returns s ∩ t.
func (s NodeIDSet) Intersect(t NodeIDSet) NodeIDSet { return s & t }

// Difference returns s \ t (elements in s that are not in t).
func (s NodeIDSet) Difference(t NodeIDSet) NodeIDSet { return s &^ t }

// SymDiff returns (s \ t) ∪ (t \ s).
func (s NodeIDSet) SymDiff(t NodeIDSet) NodeIDSet { return s ^ t }

// IsSubsetOf reports whether s ⊆ t.
func (s NodeIDSet) IsSubsetOf(t NodeIDSet) bool { return s&^t == 0 }

// Equals reports whether s == t.
func (s NodeIDSet) Equals(t NodeIDSet) bool { return s == t }

// Overlaps reports whether s ∩ t ≠ ∅.
func (s NodeIDSet) Overlaps(t NodeIDSet) bool { return s&t != 0 }

// IsDisjointWith reports whether s ∩ t == ∅.
func (s NodeIDSet) IsDisjointWith(t NodeIDSet) bool { return s&t == 0 }

// ---- Helpers for collections

// NodeIDer is implemented by types that have a NodeID.
type NodeIDer interface {
	NodeID() uint8
}

// FromAll returns a new NodeIDSet containing node IDs from all items.
func FromAll[T NodeIDer](items []T) NodeIDSet {
	var s NodeIDSet
	for i := range items {
		s.Add(items[i].NodeID())
	}
	return s
}

// FromWhere returns a new NodeIDSet containing node IDs from items that match the predicate.
func FromWhere[T NodeIDer](items []T, pred func(T) bool) NodeIDSet {
	var s NodeIDSet
	for i := range items {
		if pred(items[i]) {
			s.Add(items[i].NodeID())
		}
	}
	return s
}
