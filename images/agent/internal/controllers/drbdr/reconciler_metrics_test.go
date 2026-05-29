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

package drbdr

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestComputeDRBDRMetricObservationsRecordsConfiguredTransitionAndGauge(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := start.Add(20 * time.Second)

	statusBase := &v1alpha1.DRBDResource{
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionFalse,
				},
			},
		},
	}
	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "drbdr-a",
			CreationTimestamp: start,
			Labels: map[string]string{
				v1alpha1.ReplicatedVolumeLabelKey: "rv-a",
			},
		},
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	observations := computeDRBDRMetricObservations(now, drbdr, statusBase)
	if len(observations) != 2 {
		t.Fatalf("expected Configured transition and gauge observations, got %d", len(observations))
	}
}

func TestComputeDRBDRMetricObservationsDoesNotRepeatConfiguredTransition(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 20, 0, time.UTC)
	statusBase := &v1alpha1.DRBDResource{
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	drbdr := statusBase.DeepCopy()

	observations := computeDRBDRMetricObservations(now, drbdr, statusBase)
	if len(observations) != 1 {
		t.Fatalf("expected only gauge observation, got %d", len(observations))
	}
}
