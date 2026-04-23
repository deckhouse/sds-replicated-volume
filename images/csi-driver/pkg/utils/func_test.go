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

package utils //nolint:revive

import (
	"testing"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestIsReplicatedVolumePastFormation(t *testing.T) {
	tests := []struct {
		name string
		rv   *srv.ReplicatedVolume
		want bool
	}{
		{
			name: "nil RV",
			rv:   nil,
			want: false,
		},
		{
			name: "zero DatameshRevision and no transitions",
			rv:   &srv.ReplicatedVolume{},
			want: false,
		},
		{
			name: "zero DatameshRevision with Formation transition",
			rv: &srv.ReplicatedVolume{
				Status: srv.ReplicatedVolumeStatus{
					DatameshRevision: 0,
					DatameshTransitions: []srv.ReplicatedVolumeDatameshTransition{
						{Type: srv.ReplicatedVolumeDatameshTransitionTypeFormation},
					},
				},
			},
			want: false,
		},
		{
			name: "revision>0 with Formation transition still in progress",
			rv: &srv.ReplicatedVolume{
				Status: srv.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					DatameshTransitions: []srv.ReplicatedVolumeDatameshTransition{
						{Type: srv.ReplicatedVolumeDatameshTransitionTypeFormation},
					},
				},
			},
			want: false,
		},
		{
			name: "revision>0 and no Formation transition",
			rv: &srv.ReplicatedVolume{
				Status: srv.ReplicatedVolumeStatus{
					DatameshRevision: 1,
				},
			},
			want: true,
		},
		{
			name: "revision>0 with unrelated transition (AddReplica)",
			rv: &srv.ReplicatedVolume{
				Status: srv.ReplicatedVolumeStatus{
					DatameshRevision: 5,
					DatameshTransitions: []srv.ReplicatedVolumeDatameshTransition{
						{Type: srv.ReplicatedVolumeDatameshTransitionTypeAddReplica},
					},
				},
			},
			want: true,
		},
		{
			name: "revision>0 with AddReplica and Formation mixed (still forming)",
			rv: &srv.ReplicatedVolume{
				Status: srv.ReplicatedVolumeStatus{
					DatameshRevision: 5,
					DatameshTransitions: []srv.ReplicatedVolumeDatameshTransition{
						{Type: srv.ReplicatedVolumeDatameshTransitionTypeAddReplica},
						{Type: srv.ReplicatedVolumeDatameshTransitionTypeFormation},
					},
				},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReplicatedVolumePastFormation(tc.rv); got != tc.want {
				t.Fatalf("IsReplicatedVolumePastFormation = %v, want %v", got, tc.want)
			}
		})
	}
}
