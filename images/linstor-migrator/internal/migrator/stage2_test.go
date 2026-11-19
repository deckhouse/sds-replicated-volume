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
	"context"
	"log/slog"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

func TestClearDRBDRMaintenanceIfSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode srvv1alpha1.MaintenanceMode
		want srvv1alpha1.MaintenanceMode
	}{
		{
			name: "clears the mode stage 1 set",
			mode: srvv1alpha1.MaintenanceModeNoResourceReconciliation,
			want: "",
		},
		{
			name: "leaves a pause propagated from the volume alone",
			mode: srvv1alpha1.MaintenanceModeNoResourceReconciliationByVolume,
			want: srvv1alpha1.MaintenanceModeNoResourceReconciliationByVolume,
		},
		{
			name: "leaves an unpaused resource alone",
			mode: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			drbdr := &srvv1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec:       srvv1alpha1.DRBDResourceSpec{Maintenance: tt.mode},
			}

			scheme := runtime.NewScheme()
			_ = clientgoscheme.AddToScheme(scheme)
			_ = srvv1alpha1.AddToScheme(scheme)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(drbdr).Build()

			m := &Migrator{client: cl, log: slog.Default()}
			if err := m.clearDRBDRMaintenanceIfSet(context.Background(), drbdr); err != nil {
				t.Fatalf("clearDRBDRMaintenanceIfSet() error = %v", err)
			}

			got := &srvv1alpha1.DRBDResource{}
			if err := cl.Get(context.Background(), types.NamespacedName{Name: "rvr-1"}, got); err != nil {
				t.Fatalf("get DRBDResource: %v", err)
			}
			if got.Spec.Maintenance != tt.want {
				t.Fatalf("maintenance = %q, want %q", got.Spec.Maintenance, tt.want)
			}
		})
	}
}

func TestStage2PatchesLeaveMaskedSharedSecretOut(t *testing.T) {
	t.Parallel()

	const masked = "<omitted>"

	drbdr := &srvv1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		Spec: srvv1alpha1.DRBDResourceSpec{
			Maintenance: srvv1alpha1.MaintenanceModeNoResourceReconciliation,
			Peers:       []srvv1alpha1.DRBDResourcePeer{{Name: "rvr-2", NodeID: 2, SharedSecret: masked}},
		},
	}
	rv := &srvv1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rv-1",
			Annotations: map[string]string{
				srvv1alpha1.AdoptRVRAnnotationKey:          "",
				srvv1alpha1.AdoptSharedSecretAnnotationKey: "linstor-secret",
			},
		},
		Status: srvv1alpha1.ReplicatedVolumeStatus{
			Datamesh: srvv1alpha1.ReplicatedVolumeDatamesh{SharedSecret: masked},
		},
	}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = srvv1alpha1.AddToScheme(scheme)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(drbdr, rv).Build()

	var patches []string
	cl := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, c kubecl.WithWatch, obj kubecl.Object, patch kubecl.Patch, opts ...kubecl.PatchOption) error {
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			patches = append(patches, string(data))
			return c.Patch(ctx, obj, patch, opts...)
		},
		Update: func(_ context.Context, _ kubecl.WithWatch, obj kubecl.Object, _ ...kubecl.UpdateOption) error {
			t.Errorf("stage 2 updates %T %s; an update sends the whole object, a masked shared secret included", obj, obj.GetName())
			return nil
		},
	})

	m := &Migrator{client: cl, log: slog.Default()}
	if err := m.clearDRBDRMaintenanceIfSet(context.Background(), drbdr); err != nil {
		t.Fatalf("clearDRBDRMaintenanceIfSet() error = %v", err)
	}
	if err := m.patchRVRemoveAdoptAnnotations(context.Background(), rv); err != nil {
		t.Fatalf("patchRVRemoveAdoptAnnotations() error = %v", err)
	}

	if len(patches) != 2 {
		t.Fatalf("got %d patches, want 2: %v", len(patches), patches)
	}
	for _, p := range patches {
		if strings.Contains(p, "sharedSecret") || strings.Contains(p, masked) {
			t.Errorf("patch carries the shared secret: %s", p)
		}
	}
}
