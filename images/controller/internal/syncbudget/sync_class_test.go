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

package syncbudget_test

import (
	"testing"

	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

// newRV builds a ReplicatedVolume carrying only what the class ladder reads: the
// configured FTT/GMDR (which give D) and the effective layout D is compared against.
func newRV(ftt, gmdr byte, upToDateVoters int8, effectiveGMDR *int8) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate:              ftt,
				GuaranteedMinimumDataRedundancy: gmdr,
			},
			EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
				UpToDateVoters:                  upToDateVoters,
				GuaranteedMinimumDataRedundancy: effectiveGMDR,
			},
		},
	}
}

// Every rung needs its own stable string and no input may ever yield an empty name.
func TestClassString(t *testing.T) {
	tests := []struct {
		class syncbudget.Class
		want  string
	}{
		{syncbudget.ClassIOBlocked, "IOBlocked"},
		{syncbudget.ClassDeficit, "Deficit"},
		{syncbudget.ClassNone, "None"},
	}

	seen := make(map[string]bool, len(tests))
	for _, tt := range tests {
		got := tt.class.String()
		if got != tt.want {
			t.Errorf("Class(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
		if seen[got] {
			t.Errorf("label %q is used by more than one rung", got)
		}
		seen[got] = true
	}

	if got := syncbudget.Class(200).String(); got == "" {
		t.Error("a class outside the ladder produced an empty name")
	}
}

// A volume mid-formation has no UpToDate copy by construction, so it lands on the top rung
// alongside a living volume that lost every copy. Keeping formation out of the queue is the
// scheduler's job, not the ladder's — there is deliberately no formation branch here.
func TestComputeActualSyncClassIgnoresFormation(t *testing.T) {
	forming := newRV(1, 1, 0, ptr.To(int8(-1)))
	forming.Status.DatameshRevision = 0

	living := newRV(1, 1, 0, ptr.To(int8(-1)))
	living.Status.DatameshRevision = 7

	if got := syncbudget.ComputeActualSyncClass(forming); got != syncbudget.ClassIOBlocked {
		t.Errorf("forming volume = %v, want %v", got, syncbudget.ClassIOBlocked)
	}
	if got := syncbudget.ComputeActualSyncClass(living); got != syncbudget.ClassIOBlocked {
		t.Errorf("living volume that lost every copy = %v, want %v", got, syncbudget.ClassIOBlocked)
	}
}

func TestComputeActualSyncClass(t *testing.T) {
	tests := []struct {
		name string
		rv   *v1alpha1.ReplicatedVolume
		want syncbudget.Class
	}{
		{
			// FTT1+GMDR1 gives D=3. Three UpToDate voters and a non-negative
			// effective GMDR mean the volume holds every copy it is owed.
			name: "every copy up to date — no slot needed",
			rv:   newRV(1, 1, 3, ptr.To(int8(1))),
			want: syncbudget.ClassNone,
		},
		{
			// FTT0+GMDR0 gives D=1: a single-replica volume holding its one copy. Its
			// effective GMDR is exactly 0, which also pins the rung above — zero is not
			// below zero, so it is not IO-blocked.
			name: "single-replica volume holding its one copy — no slot needed",
			rv:   newRV(0, 0, 1, ptr.To(int8(0))),
			want: syncbudget.ClassNone,
		},
		{
			// D=3 but only two copies carry data. Effective GMDR is still
			// non-negative, so IO runs and this is a deficit, not an outage.
			name: "one copy short of D — deficit",
			rv:   newRV(1, 1, 2, ptr.To(int8(1))),
			want: syncbudget.ClassDeficit,
		},
		{
			// No UpToDate voter left drives effective GMDR below zero, which is
			// what "IO is stopped" means. It outranks a plain deficit.
			name: "no up-to-date copy left — io blocked outranks deficit",
			rv:   newRV(1, 1, 0, ptr.To(int8(-1))),
			want: syncbudget.ClassIOBlocked,
		},
		{
			// A nil effective GMDR means "no fresh agent data", not "bad": a silent agent
			// must not buy the top rung.
			name: "effective gmdr unknown — not io blocked, falls through to deficit",
			rv:   newRV(1, 1, 0, nil),
			want: syncbudget.ClassDeficit,
		},
		{
			name: "effective gmdr unknown but all copies held — no slot needed",
			rv:   newRV(1, 1, 3, nil),
			want: syncbudget.ClassNone,
		},
		{
			// Without a resolved configuration there is no D, so there is no count to
			// fall short of. The scheduler already returns early on such a volume; the
			// ladder must still be total rather than panic.
			name: "no resolved configuration — no claim to make",
			rv:   &v1alpha1.ReplicatedVolume{},
			want: syncbudget.ClassNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncbudget.ComputeActualSyncClass(tt.rv); got != tt.want {
				t.Errorf("ComputeActualSyncClass() = %v, want %v", got, tt.want)
			}
		})
	}
}
