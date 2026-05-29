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

package rvcontroller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestComputeDatameshMetricObservations(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := metav1.NewTime(start.Add(10 * time.Second))
	now := start.Add(20 * time.Second)

	rv := &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-a",
		},
	}

	newTransition := func(status v1alpha1.ReplicatedVolumeDatameshTransitionStepStatus, completedAt *metav1.Time) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{
					Name:        "Formation",
					Status:      status,
					StartedAt:   &start,
					CompletedAt: completedAt,
				},
			},
		}
	}

	t.Run("no diff produces no observations", func(t *testing.T) {
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil)
		if len(observations) != 0 {
			t.Fatalf("expected no observations, got %d", len(observations))
		}
	})

	t.Run("completed transition records transition and step durations", func(t *testing.T) {
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = nil

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil)
		if len(observations) != 2 {
			t.Fatalf("expected transition and step observations, got %d", len(observations))
		}
	})

	t.Run("completed active step records only step duration", func(t *testing.T) {
		activeOld := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, nil)
		activeNew := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{activeNew}

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{activeOld}, nil)
		if len(observations) != 1 {
			t.Fatalf("expected one step observation, got %d", len(observations))
		}
	})
}

func TestDatameshMetricLabels(t *testing.T) {
	rv := &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-a",
		},
	}

	cases := []struct {
		name       string
		transition v1alpha1.ReplicatedVolumeDatameshTransition
		rvrs       []*v1alpha1.ReplicatedVolumeReplica
		wantNode   string
	}{
		{
			name: "global transition",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			},
			wantNode: "global",
		},
		{
			name: "known replica node",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
				ReplicaName: "rvr-a",
			},
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
				},
			},
			wantNode: "node-a",
		},
		{
			name: "unknown replica node",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
				ReplicaName: "missing-rvr",
			},
			wantNode: "unknown",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			storageClass, node, typ := datameshMetricLabels(rv, &tt.transition, datameshReplicaNodes(tt.rvrs))
			if storageClass != "sc-a" || node != tt.wantNode || typ != string(tt.transition.Type) {
				t.Fatalf("unexpected labels: storageClass=%q node=%q type=%q", storageClass, node, typ)
			}
		})
	}
}
