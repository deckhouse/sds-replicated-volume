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

package idset

import (
	"iter"
	"math/bits"
)

// idStr is a lookup table for ID string representations (0-31).
var idStr = [32]string{
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
	"20", "21", "22", "23", "24", "25", "26", "27", "28", "29",
	"30", "31",
}

// IDSet is a bitset for IDs (0-31).
type IDSet uint32

// All is an IDSet with all 32 IDs present.
const All IDSet = 0xFFFF_FFFF

// Of returns a singleton IDSet containing only the given ID.
func Of(id uint8) IDSet { return 1 << id }

// Contains reports whether the set contains id.
func (s IDSet) Contains(id uint8) bool { return s&(1<<id) != 0 }

// Add adds id to the set.
func (s *IDSet) Add(id uint8) { *s |= 1 << id }

// Remove removes id from the set.
func (s *IDSet) Remove(id uint8) { *s &^= 1 << id }

// IsEmpty reports whether the set is empty.
func (s IDSet) IsEmpty() bool { return s == 0 }

// Len returns the number of IDs in the set.
func (s IDSet) Len() int { return bits.OnesCount32(uint32(s)) }

// Min returns the smallest ID in the set.
// Panics if the set is empty.
func (s IDSet) Min() uint8 { return uint8(bits.TrailingZeros32(uint32(s))) }

// Max returns the largest ID in the set.
// Panics if the set is empty.
func (s IDSet) Max() uint8 { return uint8(31 - bits.LeadingZeros32(uint32(s))) }

// MinMissing returns the smallest ID that is not in the set.
// Returns (id, true) if found, or (0, false) if all 32 IDs are present.
func (s IDSet) MinMissing() (uint8, bool) {
	inv := ^uint32(s)
	if inv == 0 {
		return 0, false
	}
	return uint8(bits.TrailingZeros32(inv)), true
}

// All returns an iterator (iter.Seq) over IDs in the set (ascending).
// Usage: for id := range s.All() { ... }
func (s IDSet) All() iter.Seq[uint8] {
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

// AppendTo appends all set IDs to dst (ascending) and returns dst.
func (s IDSet) AppendTo(dst []uint8) []uint8 {
	for id := range s.All() {
		dst = append(dst, id)
	}
	return dst
}

// String returns a comma-separated list of IDs with # prefix (e.g., "#0, #3, #7").
// Uses a fixed-size stack buffer; only one allocation for the final string.
func (s IDSet) String() string {
	if s == 0 {
		return ""
	}
	// Max capacity: 32 '#' + 10 one-digit (0-9) + 22 two-digit (10-31) + 31 separators (", ") = 148 bytes.
	var buf [148]byte
	b := buf[:0]
	x := uint32(s)
	for x != 0 {
		if len(b) > 0 {
			b = append(b, ',', ' ')
		}
		i := bits.TrailingZeros32(x)
		b = append(b, '#')
		b = append(b, idStr[i]...)
		x &^= 1 << uint(i)
	}
	return string(b)
}

// ---- Set algebra

// Union returns s ∪ t.
func (s IDSet) Union(t IDSet) IDSet { return s | t }

// Intersect returns s ∩ t.
func (s IDSet) Intersect(t IDSet) IDSet { return s & t }

// Difference returns s \ t (elements in s that are not in t).
func (s IDSet) Difference(t IDSet) IDSet { return s &^ t }

// SymDiff returns (s \ t) ∪ (t \ s).
func (s IDSet) SymDiff(t IDSet) IDSet { return s ^ t }

// IsSubsetOf reports whether s ⊆ t.
func (s IDSet) IsSubsetOf(t IDSet) bool { return s&^t == 0 }

// Equals reports whether s == t.
func (s IDSet) Equals(t IDSet) bool { return s == t }

// Overlaps reports whether s ∩ t ≠ ∅.
func (s IDSet) Overlaps(t IDSet) bool { return s&t != 0 }

// IsDisjointWith reports whether s ∩ t == ∅.
func (s IDSet) IsDisjointWith(t IDSet) bool { return s&t == 0 }

// ---- Helpers for collections

// IDer is implemented by types that have an ID.
type IDer interface {
	ID() uint8
}

// FromAll returns a new IDSet containing IDs from all items.
func FromAll[T IDer](items []T) IDSet {
	var s IDSet
	for i := range items {
		s.Add(items[i].ID())
	}
	return s
}

// FromWhere returns a new IDSet containing IDs from items that match the predicate.
func FromWhere[T IDer](items []T, pred func(T) bool) IDSet {
	var s IDSet
	for i := range items {
		if pred(items[i]) {
			s.Add(items[i].ID())
		}
	}
	return s
}

// FromFunc returns a new IDSet by calling fn for each item.
// fn returns the ID and whether to include it.
func FromFunc[T any](items []T, fn func(T) (id uint8, include bool)) IDSet {
	var s IDSet
	for i := range items {
		if id, ok := fn(items[i]); ok {
			s.Add(id)
		}
	}
	return s
}
