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

package rvrcontroller

import "math/bits"

// NodeIDSet is a bitset for node IDs (0-31).
type NodeIDSet uint32

// Has returns true if the node ID is in the set.
func (s NodeIDSet) Has(id uint8) bool { return s&(1<<id) != 0 }

// Add adds a node ID to the set.
func (s *NodeIDSet) Add(id uint8) { *s |= 1 << id }

// Del removes a node ID from the set.
func (s *NodeIDSet) Del(id uint8) { *s &^= 1 << id }

// IsEmpty returns true if the set is empty.
func (s NodeIDSet) IsEmpty() bool { return s == 0 }

// ForEach calls fn for each node ID in the set.
func (s NodeIDSet) ForEach(fn func(id uint8)) {
	x := uint32(s)
	for x != 0 {
		i := bits.TrailingZeros32(x)
		fn(uint8(i))
		x &^= 1 << uint(i)
	}
}

// ToSlice appends all set node IDs to dst and returns the result.
func (s NodeIDSet) ToSlice(dst []uint8) []uint8 {
	x := uint32(s)
	for x != 0 {
		i := bits.TrailingZeros32(x)
		dst = append(dst, uint8(i))
		x &^= 1 << uint(i)
	}
	return dst
}
