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
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

func TestHealthStatusFromPointer(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		if got := healthStatusFromPointer(nil); got != healthStatusUnknown {
			t.Fatalf("expected %q, got %q", healthStatusUnknown, got)
		}
	})

	t.Run("healthy", func(t *testing.T) {
		value := int8(0)
		if got := healthStatusFromPointer(&value); got != healthStatusHealthy {
			t.Fatalf("expected %q, got %q", healthStatusHealthy, got)
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		value := int8(-1)
		if got := healthStatusFromPointer(&value); got != healthStatusUnhealthy {
			t.Fatalf("expected %q, got %q", healthStatusUnhealthy, got)
		}
	})
}

func TestVolumeCheckerKeepsUnknownAsFinalHealthWithoutTransition(t *testing.T) {
	stats := &CheckerStats{RVName: "rv-1"}
	checker := NewVolumeChecker("rv-1", nil, stats)

	checker.processRVUpdate(&v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{},
		},
	})

	if got := HealthStatusString(stats.LastFTTStatus.Load()); got != "unknown" {
		t.Fatalf("expected final FTT status unknown, got %q", got)
	}
	if got := stats.FTTTransitions.Load(); got != 0 {
		t.Fatalf("expected no FTT transition for unknown state, got %d", got)
	}
}

func TestVolumeCheckerRecoversAfterUnknownWithoutBrokenParity(t *testing.T) {
	stats := &CheckerStats{RVName: "rv-1"}
	checker := NewVolumeChecker("rv-1", &kubeutils.Client{}, stats)

	checker.processRVUpdate(newRVWithEffectiveFTT(nil))

	unhealthy := int8(-1)
	checker.processRVUpdate(newRVWithEffectiveFTT(&unhealthy))

	healthy := int8(0)
	checker.processRVUpdate(newRVWithEffectiveFTT(&healthy))

	if got := HealthStatusString(stats.LastFTTStatus.Load()); got != "healthy" {
		t.Fatalf("expected final FTT status healthy, got %q", got)
	}
	if got := stats.FTTTransitions.Load(); got != 1 {
		t.Fatalf("expected one counted FTT transition from unhealthy to healthy, got %d", got)
	}
}

func newRVWithEffectiveFTT(ftt *int8) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
				FailuresToTolerate: ftt,
			},
		},
	}
}
