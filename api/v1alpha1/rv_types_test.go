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

package v1alpha1_test

import (
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestIntendedLayout(t *testing.T) {
	cases := []struct {
		name            string
		ftt, gmdr       byte
		wantDiskful     int
		wantTieBreakers int
	}{
		// Legacy replication presets (see replicationToFTTGMDR).
		{"None (FTT=0,GMDR=0)", 0, 0, 1, 0},
		{"Availability (FTT=1,GMDR=0)", 1, 0, 2, 1},
		{"Consistency (FTT=0,GMDR=1)", 0, 1, 2, 0},
		{"ConsistencyAndAvailability (FTT=1,GMDR=1)", 1, 1, 3, 0},
		// Manual FTT/GMDR combinations (|FTT-GMDR| <= 1).
		{"Manual FTT=1,GMDR=2", 1, 2, 4, 0},
		{"Manual FTT=2,GMDR=1", 2, 1, 4, 1},
		{"Manual FTT=2,GMDR=2", 2, 2, 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate:              tc.ftt,
				GuaranteedMinimumDataRedundancy: tc.gmdr,
			}
			gotDiskful, gotTieBreakers := cfg.IntendedLayout()
			if gotDiskful != tc.wantDiskful || gotTieBreakers != tc.wantTieBreakers {
				t.Errorf("IntendedLayout() = (%dD, %dTB), want (%dD, %dTB)",
					gotDiskful, gotTieBreakers, tc.wantDiskful, tc.wantTieBreakers)
			}
		})
	}
}

func TestTieBreakersForDiskful(t *testing.T) {
	cases := []struct {
		name         string
		diskful, ftt int
		want         int
	}{
		{"zero diskful", 0, 0, 0},
		{"odd diskful (1D)", 1, 0, 0},
		{"odd diskful (3D)", 3, 1, 0},
		{"even diskful, FTT==D/2 (2D, FTT=1)", 2, 1, 1},
		{"even diskful, FTT<D/2 (4D, FTT=1)", 4, 1, 0},
		{"even diskful, FTT==D/2 (4D, FTT=2)", 4, 2, 1},
		{"even diskful, FTT>D/2 (2D, FTT=2)", 2, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v1alpha1.TieBreakersForDiskful(tc.diskful, tc.ftt); got != tc.want {
				t.Errorf("TieBreakersForDiskful(%d, %d) = %d, want %d", tc.diskful, tc.ftt, got, tc.want)
			}
		})
	}
}
