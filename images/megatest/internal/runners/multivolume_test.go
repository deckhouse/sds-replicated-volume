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

package runners

import (
	"slices"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
)

func TestRVSizeBucketsMi(t *testing.T) {
	tests := []struct {
		name   string
		minMi  int64
		maxMi  int64
		stepMi int64
		want   []int64
	}{
		{
			name:   "inclusive default step",
			minMi:  100,
			maxMi:  500,
			stepMi: 100,
			want:   []int64{100, 200, 300, 400, 500},
		},
		{
			name:   "includes max when range is not divisible by step",
			minMi:  100,
			maxMi:  450,
			stepMi: 100,
			want:   []int64{100, 200, 300, 400, 450},
		},
		{
			name:   "wide step keeps both edges",
			minMi:  100,
			maxMi:  150,
			stepMi: 100,
			want:   []int64{100, 150},
		},
		{
			name:   "min greater than max collapses to max",
			minMi:  500,
			maxMi:  100,
			stepMi: 100,
			want:   []int64{100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rvSizeBucketsMi(tt.minMi, tt.maxMi, tt.stepMi)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestResolveRVSizeFixed(t *testing.T) {
	m := &MultiVolume{
		cfg: config.MultiVolumeConfig{
			RVSize: config.RVSizeConfig{MinMi: 512, MaxMi: 512, StepMi: 100},
		},
	}

	size := m.resolveRVSize()
	if got := size.String(); got != "512Mi" {
		t.Fatalf("expected 512Mi, got %q", got)
	}
}
