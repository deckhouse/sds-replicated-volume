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

package rvscontroller

import (
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func mkRVR(name, nodeName string, rType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			Type:     rType,
			NodeName: nodeName,
		},
	}
}

func init() {
	_ = mkRVR
}

func mkRVRWithName(name, nodeName string, rType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	rvr := mkRVR(name, nodeName, rType)
	rvr.Name = name
	return rvr
}

func mkMember(name string, attached bool) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{
		Name:     name,
		Attached: attached,
	}
}

func TestSplitRVRsByAttachment(t *testing.T) {
	tests := []struct {
		name          string
		rvrs          []*v1alpha1.ReplicatedVolumeReplica
		members       []v1alpha1.DatameshMember
		wantSecondary []string
		wantPrimary   []string
	}{
		{
			name: "all secondary (no attached members)",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", false),
				mkMember("rvr-1", false),
			},
			wantSecondary: []string{"rvr-0", "rvr-1"},
			wantPrimary:   nil,
		},
		{
			name: "one primary, one secondary",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", true),
				mkMember("rvr-1", false),
			},
			wantSecondary: []string{"rvr-1"},
			wantPrimary:   []string{"rvr-0"},
		},
		{
			name: "skips non-diskful and empty nodeName",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-2", "node-c", v1alpha1.ReplicaTypeTieBreaker),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", false),
			},
			wantSecondary: []string{"rvr-0"},
			wantPrimary:   nil,
		},
		{
			name:          "empty input",
			rvrs:          nil,
			members:       nil,
			wantSecondary: nil,
			wantPrimary:   nil,
		},
		{
			name: "multiple attached (multi-attach)",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-2", "node-c", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", true),
				mkMember("rvr-1", true),
				mkMember("rvr-2", false),
			},
			wantSecondary: []string{"rvr-2"},
			wantPrimary:   []string{"rvr-0", "rvr-1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secondary, primary := splitRVRsByAttachment(tc.rvrs, tc.members)

			secNames := rvrNames(secondary)
			priNames := rvrNames(primary)

			if !strSliceEqual(secNames, tc.wantSecondary) {
				t.Errorf("secondary = %v, want %v", secNames, tc.wantSecondary)
			}
			if !strSliceEqual(priNames, tc.wantPrimary) {
				t.Errorf("primary = %v, want %v", priNames, tc.wantPrimary)
			}
		})
	}
}

func TestAllRVRSReady(t *testing.T) {
	readyRVRS := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
		},
	}
	pendingRVRS := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhasePending,
		},
	}

	tests := []struct {
		name          string
		rvrs          []*v1alpha1.ReplicatedVolumeReplica
		existingByRVR map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot
		want          bool
	}{
		{
			name:          "empty list",
			rvrs:          nil,
			existingByRVR: nil,
			want:          true,
		},
		{
			name: "all ready",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{
				"rvr-0": readyRVRS,
				"rvr-1": readyRVRS,
			},
			want: true,
		},
		{
			name: "one not ready",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{
				"rvr-0": readyRVRS,
				"rvr-1": pendingRVRS,
			},
			want: false,
		},
		{
			name: "rvrs not yet created",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{},
			want:          false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := allRVRSReady(tc.rvrs, tc.existingByRVR)
			if got != tc.want {
				t.Errorf("allRVRSReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func rvrNames(rvrs []*v1alpha1.ReplicatedVolumeReplica) []string {
	if len(rvrs) == 0 {
		return nil
	}
	names := make([]string, len(rvrs))
	for i, rvr := range rvrs {
		names[i] = rvr.Name
	}
	return names
}

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
