/*
Copyright 2025 Flant JSC

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
	"testing"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestMatchesExitGate(t *testing.T) {
	t.Parallel()

	step := func(name string, st srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatus) srvv1alpha1.ReplicatedVolumeDatameshTransitionStep {
		return srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{Name: name, Status: st}
	}

	tests := []struct {
		name string
		rv   *srvv1alpha1.ReplicatedVolume
		want bool
	}{
		{
			name: "empty status",
			rv:   &srvv1alpha1.ReplicatedVolume{},
			want: false,
		},
		{
			name: "wrong plan",
			rv: &srvv1alpha1.ReplicatedVolume{
				Status: srvv1alpha1.ReplicatedVolumeStatus{
					DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
						{
							Type:   srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							PlanID: "create/v1",
							Steps: []srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{
								step("a", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("b", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("c", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive),
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "adopt gate satisfied",
			rv: &srvv1alpha1.ReplicatedVolume{
				Status: srvv1alpha1.ReplicatedVolumeStatus{
					DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
						{
							Type:   srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							PlanID: formationPlanAdopt,
							Steps: []srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{
								step("Verify prerequisites", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("Populate and verify datamesh", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("Exit maintenance", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive),
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "second transition is formation adopt",
			rv: &srvv1alpha1.ReplicatedVolume{
				Status: srvv1alpha1.ReplicatedVolumeStatus{
					DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
						{Type: srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica},
						{
							Type:   srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							PlanID: formationPlanAdopt,
							Steps: []srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{
								step("s0", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("s1", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("s2", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive),
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "wrong step count",
			rv: &srvv1alpha1.ReplicatedVolume{
				Status: srvv1alpha1.ReplicatedVolumeStatus{
					DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
						{
							Type:   srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							PlanID: formationPlanAdopt,
							Steps: []srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{
								step("s0", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("s1", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive),
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "third step not active",
			rv: &srvv1alpha1.ReplicatedVolume{
				Status: srvv1alpha1.ReplicatedVolumeStatus{
					DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
						{
							Type:   srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							PlanID: formationPlanAdopt,
							Steps: []srvv1alpha1.ReplicatedVolumeDatameshTransitionStep{
								step("s0", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("s1", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted),
								step("s2", srvv1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending),
							},
						},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesExitGate(tt.rv); got != tt.want {
				t.Fatalf("matchesExitGate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindFormationAdoptTransition(t *testing.T) {
	t.Parallel()
	rv := &srvv1alpha1.ReplicatedVolume{
		Status: srvv1alpha1.ReplicatedVolumeStatus{
			DatameshTransitions: []srvv1alpha1.ReplicatedVolumeDatameshTransition{
				{Type: srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, PlanID: "create/v1"},
				{Type: srvv1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, PlanID: formationPlanAdopt},
			},
		},
	}
	tr := findFormationAdoptTransition(rv)
	if tr == nil {
		t.Fatal("expected transition")
	}
	if tr.PlanID != formationPlanAdopt {
		t.Fatalf("PlanID = %q", tr.PlanID)
	}
}
