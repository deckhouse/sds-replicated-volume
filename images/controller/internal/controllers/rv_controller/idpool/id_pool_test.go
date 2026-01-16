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

package idpool_test

import (
	"fmt"
	"reflect"
	"testing"

	. "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/idpool"
)

type id0_3 uint32

func (id0_3) Min() uint32 { return 0 }
func (id0_3) Max() uint32 { return 3 }

type id0_7 uint32

func (id0_7) Min() uint32 { return 0 }
func (id0_7) Max() uint32 { return 7 }

type id0_10 uint32

func (id0_10) Min() uint32 { return 0 }
func (id0_10) Max() uint32 { return 10 }

type id0_2048 uint32

func (id0_2048) Min() uint32 { return 0 }
func (id0_2048) Max() uint32 { return 2048 }

type id100_102 uint32

func (id100_102) Min() uint32 { return 100 }
func (id100_102) Max() uint32 { return 102 }

type testIDPool[T Identifier] struct {
	*testing.T
	*IDPool[T]
}

func TestIDPool_GetOrCreate_MinimalReuse(t *testing.T) {
	testIDPool[id0_7]{t, NewIDPool[id0_7]()}.
		expectLen(0).
		// allocate 0..7
		getOrCreate("a", nil, 0, "").
		getOrCreate("b", nil, 1, "").
		getOrCreate("c", nil, 2, "").
		getOrCreate("d", nil, 3, "").
		getOrCreate("e", nil, 4, "").
		getOrCreate("f", nil, 5, "").
		getOrCreate("g", nil, 6, "").
		getOrCreate("h", nil, 7, "").
		expectLen(8).
		// exhausted
		getOrCreate("x", nil, 0, "IDPool: pool exhausted (range=[0..7])").
		// release some, ensure minimal ids are reused
		release("b").
		release("d").
		getOrCreate("x", nil, 1, "").
		getOrCreate("y", nil, 3, "").
		expectLen(8)
}

func TestIDPool_GetOrCreate_WithID_Conflicts(t *testing.T) {
	p := NewIDPool[id0_10]()

	// register
	{
		id := id0_10(2)
		if _, err := p.EnsureAllocated("a", &id); err != nil {
			t.Fatalf("expected EnsureAllocated to succeed, got %v", err)
		}
	}
	// idempotent
	{
		id := id0_10(2)
		if _, err := p.EnsureAllocated("a", &id); err != nil {
			t.Fatalf("expected EnsureAllocated to be idempotent, got %v", err)
		}
	}
	// name conflict
	{
		id := id0_10(3)
		if _, err := p.EnsureAllocated("a", &id); err == nil || err.Error() != `IDPool: name "a" is already mapped to id 2 (requested 3)` {
			t.Fatalf("expected NameConflictError, got %v", err)
		}
	}
	// duplicate id
	{
		id := id0_10(2)
		if _, err := p.EnsureAllocated("b", &id); err == nil || err.Error() != `IDPool: id 2 is already owned by "a"` {
			t.Fatalf("expected DuplicateIDError, got %v", err)
		}
	}
	// max exceeded
	{
		id := id0_10(11)
		if _, err := p.EnsureAllocated("x", &id); err == nil || err.Error() != `IDPool: id 11 is outside allowed range [0..10]` {
			t.Fatalf("expected OutOfRangeError, got %v", err)
		}
	}
}

func TestIDPool_Fill_OrderAndErrors(t *testing.T) {
	p := NewIDPool[id0_3]()

	errs := p.Fill([]IDNamePair[id0_3]{
		{ID: id0_3(0), Name: "a"}, // ok
		{ID: id0_3(0), Name: "b"}, // dup id -> error (owned by a)
		{ID: id0_3(1), Name: "b"}, // ok
		{ID: id0_3(1), Name: "a"}, // name conflict -> error
	})

	want := []error{
		nil,
		DuplicateIDError{ID: 0, ConflictingName: "a"},
		nil,
		NameConflictError{Name: "a", ExistingID: 0, RequestedID: 1},
	}
	if !reflect.DeepEqual(stringifyErrSlice(errs), stringifyErrSlice(want)) {
		t.Fatalf("unexpected errs slice: got=%v want=%v", stringifyErrSlice(errs), stringifyErrSlice(want))
	}

	// Ensure successful ones are present.
	if id, err := p.EnsureAllocated("a", nil); err != nil || id == nil || uint32(*id) != 0 {
		var got uint32
		if id != nil {
			got = uint32(*id)
		}
		t.Fatalf("expected a=0, got id=%d err=%v", got, err)
	}
	if id, err := p.EnsureAllocated("b", nil); err != nil || id == nil || uint32(*id) != 1 {
		var got uint32
		if id != nil {
			got = uint32(*id)
		}
		t.Fatalf("expected b=1, got id=%d err=%v", got, err)
	}
}

func TestIDPool_Release_MinimalBecomesFreeAgain(t *testing.T) {
	p := NewIDPool[id0_10]()
	if _, err := p.EnsureAllocated("a", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.Release("a")

	// Now 0 should be minimal again.
	if id, err := p.EnsureAllocated("b", nil); err != nil || id == nil || uint32(*id) != 0 {
		var got uint32
		if id != nil {
			got = uint32(*id)
		}
		t.Fatalf("expected b=0, got id=%d err=%v", got, err)
	}
}

func TestIDPool_Bitmap_SparseReservationsAcrossRange(t *testing.T) {
	const maxID = uint32(2048)
	p := NewIDPool[id0_2048]()

	// Reserve 10 ids spread across the full range, including word boundaries (63/64)
	// and the last possible id (2048) to validate bitset masking.
	reservedIDs := map[uint32]string{
		0:    "r-0",
		1:    "r-1",
		63:   "r-63",
		64:   "r-64",
		65:   "r-65",
		127:  "r-127",
		128:  "r-128",
		1023: "r-1023",
		1024: "r-1024",
		2048: "r-2048",
	}
	for id, name := range reservedIDs {
		idT := id0_2048(id)
		if _, err := p.EnsureAllocated(name, &idT); err != nil {
			t.Fatalf("expected EnsureAllocated(%q,&%d) to succeed, got %v", name, id, err)
		}
	}

	allocated := map[uint32]struct{}{}
	for {
		id, err := p.EnsureAllocated(fmt.Sprintf("free-%d", len(allocated)), nil)
		if err != nil {
			if err.Error() != "IDPool: pool exhausted (range=[0..2048])" {
				t.Fatalf("expected max exceeded error, got %v", err)
			}
			break
		}

		if id == nil {
			t.Fatalf("expected non-nil id on success")
		}
		idU := uint32(*id)
		if _, isReserved := reservedIDs[idU]; isReserved {
			t.Fatalf("allocator returned reserved id %d", idU)
		}
		if _, dup := allocated[idU]; dup {
			t.Fatalf("allocator returned duplicate id %d", idU)
		}
		allocated[idU] = struct{}{}
	}

	wantAllocated := int(maxID) + 1 - len(reservedIDs) // inclusive range size minus reserved
	if len(allocated) != wantAllocated {
		t.Fatalf("unexpected allocated count: got=%d want=%d", len(allocated), wantAllocated)
	}
}

func TestIDPool_Fill_ReturnsOutOfRangeError(t *testing.T) {
	p := NewIDPool[id0_3]()
	errs := p.Fill([]IDNamePair[id0_3]{
		{ID: id0_3(4), Name: "c"}, // exceeds -> error
	})
	if len(errs) != 1 || errs[0] == nil || errs[0].Error() != `IDPool: id 4 is outside allowed range [0..3]` {
		t.Fatalf("expected OutOfRangeError in errs[0], got %v", stringifyErrSlice(errs))
	}
}

func TestIDPool_MinOffsetRepresentation(t *testing.T) {
	p := NewIDPool[id100_102]()

	if got := p.Min(); got != 100 {
		t.Fatalf("expected Min()=100, got %d", got)
	}
	if got := p.Max(); got != 102 {
		t.Fatalf("expected Max()=102, got %d", got)
	}

	id, err := p.EnsureAllocated("a", nil)
	if err != nil || id == nil || uint32(*id) != 100 {
		var got uint32
		if id != nil {
			got = uint32(*id)
		}
		t.Fatalf("expected first allocation to be 100, got id=%d err=%v", got, err)
	}
	id, err = p.EnsureAllocated("b", nil)
	if err != nil || id == nil || uint32(*id) != 101 {
		var got uint32
		if id != nil {
			got = uint32(*id)
		}
		t.Fatalf("expected second allocation to be 101, got id=%d err=%v", got, err)
	}

	// Out of range below min.
	{
		x := id100_102(99)
		if _, err := p.EnsureAllocated("x", &x); err == nil || err.Error() != `IDPool: id 99 is outside allowed range [100..102]` {
			t.Fatalf("expected OutOfRangeError, got %v", err)
		}
	}
}

func TestIDPool_ErrorHelpers(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("wrapped: %w", err) }

	{
		base := DuplicateIDError{ID: 1, ConflictingName: "a"}
		err := wrap(base)
		if !IsDuplicateID(err) {
			t.Fatalf("expected IsDuplicateID to be true for wrapped error, got false")
		}
		got, ok := AsDuplicateID(err)
		if !ok || got.ID != base.ID || got.ConflictingName != base.ConflictingName {
			t.Fatalf("unexpected AsDuplicateID result: ok=%v got=%v want=%v", ok, got, base)
		}
	}

	{
		base := PoolExhaustedError{Min: 0, Max: 1}
		err := wrap(base)
		if !IsPoolExhausted(err) {
			t.Fatalf("expected IsPoolExhausted to be true for wrapped error, got false")
		}
		got, ok := AsPoolExhausted(err)
		if !ok || got.Min != base.Min || got.Max != base.Max {
			t.Fatalf("unexpected AsPoolExhausted result: ok=%v got=%v want=%v", ok, got, base)
		}
	}

	{
		base := NameConflictError{Name: "a", ExistingID: 1, RequestedID: 2}
		err := wrap(base)
		if !IsNameConflict(err) {
			t.Fatalf("expected IsNameConflict to be true for wrapped error, got false")
		}
		got, ok := AsNameConflict(err)
		if !ok || got.Name != base.Name || got.ExistingID != base.ExistingID || got.RequestedID != base.RequestedID {
			t.Fatalf("unexpected AsNameConflict result: ok=%v got=%v want=%v", ok, got, base)
		}
	}

	{
		base := OutOfRangeError{ID: 99, Min: 100, Max: 102}
		err := wrap(base)
		if !IsOutOfRange(err) {
			t.Fatalf("expected IsOutOfRange to be true for wrapped error, got false")
		}
		got, ok := AsOutOfRange(err)
		if !ok || got.ID != base.ID || got.Min != base.Min || got.Max != base.Max {
			t.Fatalf("unexpected AsOutOfRange result: ok=%v got=%v want=%v", ok, got, base)
		}
	}

	{
		err := wrap(fmt.Errorf("some other error"))
		if IsDuplicateID(err) || IsPoolExhausted(err) || IsNameConflict(err) || IsOutOfRange(err) {
			t.Fatalf("expected all Is* helpers to be false for non-idpool errors")
		}
	}
}

func assertPanics(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic, got none")
		}
	}()
	f()
}

func (tp testIDPool[T]) getOrCreate(name string, id *T, expectedID uint32, expectedErr string) testIDPool[T] {
	tp.Helper()
	got, err := tp.EnsureAllocated(name, id)
	if !errIsExpected(err, expectedErr) {
		tp.Fatalf("expected EnsureAllocated(%q, ...) error %q, got %v", name, expectedErr, err)
	}

	if expectedErr == "" {
		if got == nil {
			tp.Fatalf("expected EnsureAllocated(%q, ...) to return non-nil id", name)
		}
		if uint32(*got) != expectedID {
			tp.Fatalf("expected EnsureAllocated(%q, ...) id %d, got %d", name, expectedID, uint32(*got))
		}
	}
	return tp
}

func (tp testIDPool[T]) release(name string) testIDPool[T] {
	tp.Helper()
	tp.Release(name)
	return tp
}

func (tp testIDPool[T]) expectLen(expected int) testIDPool[T] {
	tp.Helper()
	got := tp.Len()
	if got != expected {
		tp.Fatalf("expected Len()=%d, got %d", expected, got)
	}
	return tp
}

func stringifyErrMap(m map[string]error) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v == nil {
			out[k] = ""
			continue
		}
		out[k] = v.Error()
	}
	return out
}

func stringifyErrSlice(s []error) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	for i, v := range s {
		if v == nil {
			out[i] = ""
			continue
		}
		out[i] = v.Error()
	}
	return out
}

func errIsExpected(err error, expected string) bool {
	if expected == "" {
		return err == nil
	}
	if err == nil {
		return false
	}
	return err.Error() == expected
}
