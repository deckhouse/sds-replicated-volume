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

package idpool

import (
	"fmt"
	"math/bits"
	"sync"
)

// Identifier is a constraint for ID types used with IDPool.
//
// Requirements:
// - underlying type is uint32 (for safe internal offset math)
// - provides a stable inclusive range via Min()/Max()
type Identifier interface {
	~uint32
	Min() uint32
	Max() uint32
}

// IDPool provides name->id allocation with minimal free id preference.
// All public methods are concurrency-safe.
//
// Semantics:
// - EnsureAllocated registers the provided (name,id) pair; conflicts are errors.
// - Fill processes pairs in-order under a single lock and returns per-name errors.
// - Release frees the id by name.
//
// The pool uses a bitset to track used IDs and a low-watermark pointer to start scanning
// for the next minimal free id. Memory for the bitset is O(range/8) bytes.
type IDPool[T Identifier] struct {
	mu sync.Mutex

	// External range: [min..max], inclusive.
	min uint32
	max uint32

	// Internal IDs are stored as offsets:
	// internal 0 == external min, internal maxOffset == external max.
	maxOffset uint32

	byName map[string]uint32 // name -> internal offset
	byID   map[uint32]string // internal offset -> name

	used       []uint64 // bitset: 1 => used
	lowestFree uint32   // internal offset hint where to start searching for a free id
}

type IDNamePair[T Identifier] struct {
	Name string
	ID   T
}

func NewIDPool[T Identifier]() *IDPool[T] {
	var zero T
	minID := zero.Min()
	maxID := zero.Max()
	if maxID <= minID {
		panic(fmt.Sprintf("idpool: invalid range [%d..%d]", minID, maxID))
	}

	maxOffset := maxID - minID
	lastWord := int(maxOffset >> 6) // /64
	return &IDPool[T]{
		min:        minID,
		max:        maxID,
		maxOffset:  maxOffset,
		byName:     map[string]uint32{},
		byID:       map[uint32]string{},
		used:       make([]uint64, lastWord+1),
		lowestFree: 0,
	}
}

// Min returns the inclusive minimum external id of this pool.
func (p *IDPool[T]) Min() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.min
}

// Max returns the inclusive maximum external id of this pool.
func (p *IDPool[T]) Max() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

// Len returns the number of currently allocated names.
func (p *IDPool[T]) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byName)
}

// EnsureAllocated ensures that name has an allocated id and returns the effective assigned id.
//
// When id is nil:
// - If name has no id yet, it allocates the minimal free id and returns it.
// - If name already has an id, it returns the existing id.
//
// When id is provided:
// - If (name,id) already exists, this is a no-op and the same id is returned.
// - If id is free, it becomes owned by name and that id is returned.
//
// Errors / panics:
// - If id is nil and there are no ids left, it returns PoolExhaustedError.
// - If id is owned by a different name, it returns DuplicateIDError.
// - If name is already mapped to a different id, it returns NameConflictError.
// - If id is outside the allowed range, it returns OutOfRangeError.
func (p *IDPool[T]) EnsureAllocated(name string, id *T) (*T, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if id == nil {
		out, err := p.getOrCreateLocked(name)
		if err != nil {
			return nil, err
		}
		return &out, nil
	}

	if err := p.addWithIDLocked(name, *id); err != nil {
		return nil, err
	}

	out := *id
	return &out, nil
}

// Fill processes pairs in-order under a single lock.
// It returns a slice of errors aligned with the input order:
// errs[i] corresponds to pairs[i] (nil means success).
func (p *IDPool[T]) Fill(pairs []IDNamePair[T]) []error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(pairs) == 0 {
		return nil
	}

	errs := make([]error, len(pairs))
	for i, pair := range pairs {
		errs[i] = p.addWithIDLocked(pair.Name, pair.ID)
	}
	return errs
}

// Release frees an allocation for name.
// If name is not found, this is a no-op.
func (p *IDPool[T]) Release(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	offset, ok := p.byName[name]
	if !ok {
		return
	}

	delete(p.byName, name)
	delete(p.byID, offset)
	p.clearUsed(offset)
	if offset < p.lowestFree {
		p.lowestFree = offset
	} else if offset == p.lowestFree {
		// id just became free; keep watermark at the minimal possible.
		p.lowestFree = offset
	}
}

func (p *IDPool[T]) getOrCreateLocked(name string) (T, error) {
	if offset, ok := p.byName[name]; ok {
		return p.externalID(offset), nil
	}

	offset, ok := p.findFreeFrom(p.lowestFree)
	if !ok {
		return 0, PoolExhaustedError{Min: p.min, Max: p.max}
	}

	p.markUsed(offset)
	p.byName[name] = offset
	p.byID[offset] = name
	p.advanceLowestFreeAfterAlloc(offset)
	return p.externalID(offset), nil
}

func (p *IDPool[T]) addWithIDLocked(name string, id T) error {
	idU32 := uint32(id)
	offset, ok := p.toOffset(idU32)
	if !ok {
		return OutOfRangeError{ID: idU32, Min: p.min, Max: p.max}
	}

	if existingID, ok := p.byName[name]; ok {
		if existingID == offset {
			return nil
		}
		return NameConflictError{Name: name, ExistingID: uint32(p.externalID(existingID)), RequestedID: idU32}
	}

	if existingName, ok := p.byID[offset]; ok {
		if existingName == name {
			// Shouldn't happen if invariants hold, but keep it idempotent.
			p.byName[name] = offset
			p.markUsed(offset)
			p.advanceLowestFreeAfterAlloc(offset)
			return nil
		}
		return DuplicateIDError{ID: idU32, ConflictingName: existingName}
	}

	// Register new mapping.
	p.byName[name] = offset
	p.byID[offset] = name
	p.markUsed(offset)
	p.advanceLowestFreeAfterAlloc(offset)
	return nil
}

func (p *IDPool[T]) advanceLowestFreeAfterAlloc(allocated uint32) {
	// If we didn't allocate the current lowest free, it remains minimal.
	if allocated != p.lowestFree {
		return
	}
	if allocated == p.maxOffset {
		// Potentially exhausted; keep watermark at max and let findFreeFrom decide.
		p.lowestFree = p.maxOffset
		return
	}
	if next, ok := p.findFreeFrom(allocated + 1); ok {
		p.lowestFree = next
		return
	}
	// No free ids left; keep watermark somewhere inside range to make the next scan short.
	p.lowestFree = p.maxOffset
}

func (p *IDPool[T]) findFreeFrom(start uint32) (uint32, bool) {
	if start > p.maxOffset {
		return 0, false
	}

	lastWord := int(p.maxOffset >> 6)
	startWord := int(start >> 6)
	startBit := uint(start & 63)

	for wi := startWord; wi <= lastWord; wi++ {
		word := p.used[wi]

		// Mask out bits below startBit for the first word.
		if wi == startWord && startBit > 0 {
			word |= (uint64(1) << startBit) - 1
		}

		validMask := ^uint64(0)
		if wi == lastWord {
			endBit := uint(p.maxOffset & 63)
			validMask = (uint64(1) << (endBit + 1)) - 1
		}

		free := (^word) & validMask
		if free == 0 {
			continue
		}
		tz := bits.TrailingZeros64(free)
		offset := uint32(wi*64 + tz)
		if offset > p.maxOffset {
			return 0, false
		}
		return offset, true
	}

	return 0, false
}

func (p *IDPool[T]) markUsed(offset uint32) {
	word := offset >> 6
	bit := offset & 63
	p.used[word] |= uint64(1) << bit
}

func (p *IDPool[T]) clearUsed(offset uint32) {
	word := offset >> 6
	bit := offset & 63
	p.used[word] &^= uint64(1) << bit
}

func (p *IDPool[T]) toOffset(external uint32) (uint32, bool) {
	if external < p.min || external > p.max {
		return 0, false
	}
	return external - p.min, true
}

func (p *IDPool[T]) externalID(offset uint32) T {
	return T(p.min + offset)
}

// PoolExhaustedError is returned when there are no ids left in the pool.
type PoolExhaustedError struct {
	Min uint32
	Max uint32
}

func (e PoolExhaustedError) Error() string {
	return fmt.Sprintf("IDPool: pool exhausted (range=[%d..%d])", e.Min, e.Max)
}

// OutOfRangeError is returned when an explicit id is outside the pool range.
type OutOfRangeError struct {
	ID  uint32
	Min uint32
	Max uint32
}

func (e OutOfRangeError) Error() string {
	return fmt.Sprintf("IDPool: id %d is outside allowed range [%d..%d]", e.ID, e.Min, e.Max)
}

// DuplicateIDError is returned when an id is already owned by another name.
type DuplicateIDError struct {
	ID              uint32
	ConflictingName string
}

func (e DuplicateIDError) Error() string {
	return fmt.Sprintf("IDPool: id %d is already owned by %q", e.ID, e.ConflictingName)
}

// NameConflictError is returned when a name is already mapped to a different id.
type NameConflictError struct {
	Name        string
	ExistingID  uint32
	RequestedID uint32
}

func (e NameConflictError) Error() string {
	return fmt.Sprintf("IDPool: name %q is already mapped to id %d (requested %d)", e.Name, e.ExistingID, e.RequestedID)
}
