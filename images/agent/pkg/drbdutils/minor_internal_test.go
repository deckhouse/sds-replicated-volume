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

package drbdutils

import (
	"os"
	"slices"
	"testing"
)

// useSysfsPaths points minor discovery at the given dirs for the duration of
// the test, restoring the package defaults afterwards.
func useSysfsPaths(t *testing.T, sysBlock, sysBDI string) {
	t.Helper()
	oldBlock, oldBDI := SysBlockPath, SysBDIPath
	t.Cleanup(func() { SysBlockPath, SysBDIPath = oldBlock, oldBDI })
	SysBlockPath, SysBDIPath = sysBlock, sysBDI
}

func TestReadUsedMinors(t *testing.T) {
	sysBlock, sysBDI := t.TempDir(), t.TempDir()
	useSysfsPaths(t, sysBlock, sysBDI)

	for _, name := range []string{
		"drbd0", "drbd7", "drbd12",
		"sda", "dm-0", "drbd", "drbdfoo", // ignored: not drbd<number>
	} {
		if err := os.Mkdir(sysBlock+"/"+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range []string{
		"147:7",                           // also in /sys/block — must be deduplicated
		"147:20",                          // leftover: the block device is already gone
		"252:0", "7:1", "1470:5", "147:x", // ignored: not a DRBD minor
	} {
		if err := os.Mkdir(sysBDI+"/"+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := readUsedMinors()
	if err != nil {
		t.Fatalf("readUsedMinors() unexpected error: %v", err)
	}

	want := []uint{0, 7, 12, 20}
	if !slices.Equal(got, want) {
		t.Errorf("readUsedMinors() = %v, want %v", got, want)
	}
}

// The bdi scan refines the block-device scan; a host without that directory
// must still be able to allocate minors.
func TestReadUsedMinorsToleratesMissingBDIDir(t *testing.T) {
	sysBlock := t.TempDir()
	useSysfsPaths(t, sysBlock, sysBlock+"/does-not-exist")

	if err := os.Mkdir(sysBlock+"/drbd3", 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := readUsedMinors()
	if err != nil {
		t.Fatalf("readUsedMinors() unexpected error: %v", err)
	}
	if want := []uint{3}; !slices.Equal(got, want) {
		t.Errorf("readUsedMinors() = %v, want %v", got, want)
	}
}

// A missing /sys/block, by contrast, is a real failure: it is the primary
// source, and proceeding without it would hand out minors blindly.
func TestReadUsedMinorsFailsOnMissingSysBlock(t *testing.T) {
	useSysfsPaths(t, t.TempDir()+"/does-not-exist", t.TempDir())

	if _, err := readUsedMinors(); err == nil {
		t.Fatal("readUsedMinors() error = nil, want error")
	}
}

func TestFirstFreeMinor(t *testing.T) {
	allMinors := make([]uint, MaxDeviceMinor+1)
	for i := range allMinors {
		allMinors[i] = uint(i)
	}

	tests := []struct {
		name       string
		usedMinors []uint
		want       uint
	}{
		{
			name:       "nothing used",
			usedMinors: nil,
			want:       0,
		},
		{
			name:       "one past the highest used",
			usedMinors: []uint{0, 1, 7},
			want:       8,
		},
		{
			// Gaps below the highest minor are deliberately not reused while
			// there is room above it: allocating past the top is cheaper than
			// hunting for holes, and a failure re-seeds anyway.
			name:       "gaps below the highest are skipped",
			usedMinors: []uint{0, 5},
			want:       6,
		},
		{
			// One past the top would be out of range, so fall back to the
			// lowest free minor.
			name:       "wraps to the lowest free minor",
			usedMinors: []uint{0, 2, MaxDeviceMinor},
			want:       1,
		},
		{
			name:       "wraps when only the top is used",
			usedMinors: []uint{MaxDeviceMinor},
			want:       0,
		},
		{
			// Signals exhaustion: ExecuteNewAutoMinor turns anything above
			// MaxDeviceMinor into ErrNewMinorNoFreeMinor.
			name:       "everything used",
			usedMinors: allMinors,
			want:       MaxDeviceMinor + 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstFreeMinor(tt.usedMinors); got != tt.want {
				t.Errorf("firstFreeMinor() = %d, want %d", got, tt.want)
			}
		})
	}
}
