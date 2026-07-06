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

package migrator

import (
	"fmt"
	"log/slog"
	"testing"
)

func TestComputeMigrationFTTGMDR(t *testing.T) {
	t.Parallel()

	m := &Migrator{}
	want := map[[2]int][2]byte{
		{0, 0}: {0, 0},
		{0, 1}: {1, 1},
		{0, 2}: {1, 1},
		{1, 0}: {0, 0},
		{1, 1}: {1, 1},
		{1, 2}: {1, 1},
		{2, 0}: {1, 0},
		{2, 1}: {1, 1},
		{2, 2}: {1, 1},
		{3, 0}: {1, 1},
		{3, 1}: {1, 1},
		{3, 2}: {1, 1},
	}

	for diskful := 0; diskful <= 3; diskful++ {
		for tieBreaker := 0; tieBreaker <= 2; tieBreaker++ {
			t.Run(fmt.Sprintf("diskful=%d,tieBreaker=%d", diskful, tieBreaker), func(t *testing.T) {
				t.Parallel()
				key := [2]int{diskful, tieBreaker}
				expected := want[key]
				gotFTT, gotGMDR := m.computeMigrationFTTGMDR(slog.Default(), "test", diskful, tieBreaker)
				if gotFTT != expected[0] || gotGMDR != expected[1] {
					t.Fatalf("computeMigrationFTTGMDR(%d, %d) = (%d, %d), want (%d, %d)",
						diskful, tieBreaker, gotFTT, gotGMDR, expected[0], expected[1])
				}
			})
		}
	}
}
