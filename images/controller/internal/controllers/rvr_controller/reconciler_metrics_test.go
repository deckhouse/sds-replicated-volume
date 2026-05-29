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

package rvrcontroller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestComputeRVRMetricObservationsRecordsReadyTransition(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	readyAt := metav1.NewTime(start.Add(10 * time.Second))

	base := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: start,
				},
			},
		},
	}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: start,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 1 {
		t.Fatalf("expected one Ready transition observation, got %d", len(observations))
	}
}

func TestComputeRVRMetricObservationsDoesNotRepeatReadyTransition(t *testing.T) {
	readyAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC))

	base := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}
	rvr := base.DeepCopy()

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 0 {
		t.Fatalf("expected no repeated Ready observation, got %d", len(observations))
	}
}

func TestComputeRVRMetricObservationsRecordsBackingVolumeReadyTransition(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := start.Add(20 * time.Second)

	base := &v1alpha1.ReplicatedVolumeReplica{}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: start,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(now, rvr, base, nil)
	if len(observations) != 1 {
		t.Fatalf("expected one BackingVolumeReady observation, got %d", len(observations))
	}
}
