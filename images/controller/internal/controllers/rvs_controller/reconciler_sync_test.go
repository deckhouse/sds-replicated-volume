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

func TestAllSyncTransitionsCompleted(t *testing.T) {
	tests := []struct {
		name string
		rvs  *v1alpha1.ReplicatedVolumeSnapshot
		want bool
	}{
		{
			name: "no transitions, revision 0",
			rvs: &v1alpha1.ReplicatedVolumeSnapshot{
				Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
					SyncRevision:    0,
					SyncTransitions: nil,
				},
			},
			want: false,
		},
		{
			name: "no transitions, revision > 0 — all completed",
			rvs: &v1alpha1.ReplicatedVolumeSnapshot{
				Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
					SyncRevision:    3,
					SyncTransitions: nil,
				},
			},
			want: true,
		},
		{
			name: "empty transitions slice, revision > 0 — all completed",
			rvs: &v1alpha1.ReplicatedVolumeSnapshot{
				Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
					SyncRevision:    1,
					SyncTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{},
				},
			},
			want: true,
		},
		{
			name: "active transitions remain",
			rvs: &v1alpha1.ReplicatedVolumeSnapshot{
				Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
					SyncRevision: 2,
					SyncTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
						{Type: "SyncSnapshot"},
					},
				},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := allSyncTransitionsCompleted(tc.rvs)
			if got != tc.want {
				t.Errorf("allSyncTransitionsCompleted() = %v, want %v", got, tc.want)
			}
		})
	}
}
