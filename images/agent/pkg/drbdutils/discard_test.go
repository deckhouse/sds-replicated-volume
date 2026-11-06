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

import "testing"

func TestSanitizeRsDiscardGranularity(t *testing.T) {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)

	tests := []struct {
		name            string
		value           uint64
		granularity     uint64
		maxDiscardBytes uint64
		want            uint
	}{
		{name: "thin pool with 64 KiB chunks", value: 64 * kib, granularity: 64 * kib, maxDiscardBytes: gib, want: 64 * kib},
		{name: "thin pool with 2 MiB chunks is capped at 1 MiB", value: 2 * mib, granularity: 2 * mib, maxDiscardBytes: 2 * gib, want: mib},
		{name: "thin pool with 4 MiB chunks is capped at 1 MiB", value: 4 * mib, granularity: 4 * mib, maxDiscardBytes: 2 * gib, want: mib},
		{name: "small max discard caps the value", value: 512 * kib, granularity: 4 * kib, maxDiscardBytes: 256 * kib, want: 256 * kib},
		{name: "value is rounded up to the granularity", value: 6 * kib, granularity: 4 * kib, maxDiscardBytes: gib, want: 8 * kib},
		{name: "cap that does not divide the granularity disables it", value: 1536 * kib, granularity: 1536 * kib, maxDiscardBytes: gib, want: 0},
		{name: "device without discard", value: 4 * kib, granularity: 4 * kib, maxDiscardBytes: 0, want: 0},
		{name: "zero granularity", value: 4 * kib, granularity: 0, maxDiscardBytes: gib, want: 0},
		{name: "zero value", value: 0, granularity: 4 * kib, maxDiscardBytes: gib, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeRsDiscardGranularity(tt.value, tt.granularity, tt.maxDiscardBytes); got != tt.want {
				t.Errorf("SanitizeRsDiscardGranularity(%d, %d, %d) = %d, want %d",
					tt.value, tt.granularity, tt.maxDiscardBytes, got, tt.want)
			}
		})
	}
}
