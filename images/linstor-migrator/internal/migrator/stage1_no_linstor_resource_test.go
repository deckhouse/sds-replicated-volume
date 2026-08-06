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

package migrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	srvlinstor "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstordb"
)

// pvWithReplicatedCSI returns a PersistentVolume named "pv-1" with the replicated CSI driver.
func pvWithReplicatedCSI() *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-1"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: config.CSIDriverReplicated},
			},
		},
	}
}

// pvWithReplicatedCSIAndLabel returns a PersistentVolume named "pv-1" with the replicated CSI
// driver and the NoLinstorResource label already set.
func pvWithReplicatedCSIAndLabel() *corev1.PersistentVolume {
	pv := pvWithReplicatedCSI()
	pv.Labels = map[string]string{
		srvv1alpha1.NoLinstorResourceLabelKey: srvv1alpha1.NoLinstorResourceLabelValue,
	}
	return pv
}

// pvWithReplicatedCSIAndWrongLabel returns a PersistentVolume named "pv-1" with the replicated
// CSI driver and the NoLinstorResource label set to a wrong value ("false"). The migrator must
// overwrite it with the canonical value "true".
func pvWithReplicatedCSIAndWrongLabel() *corev1.PersistentVolume {
	pv := pvWithReplicatedCSI()
	pv.Labels = map[string]string{
		srvv1alpha1.NoLinstorResourceLabelKey: "false",
	}
	return pv
}

func TestEnsurePVNoLinstorResourceLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		objects  []*corev1.PersistentVolume
		pvMap    map[string]corev1.PersistentVolume
		db       *linstordb.LinstorDB
		check    func(t *testing.T, m *Migrator)
		patchErr error
		wantErr  bool
	}{
		{
			name:    "pv without linstor resource gets label added",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSI()},
			pvMap:   buildPVMap(pvWithReplicatedCSI()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{},
			},
			check: func(t *testing.T, m *Migrator) {
				pv := getPV(t, m)
				if pv.Labels[srvv1alpha1.NoLinstorResourceLabelKey] != srvv1alpha1.NoLinstorResourceLabelValue {
					t.Fatalf("expected label %q to be %q, got labels=%v",
						srvv1alpha1.NoLinstorResourceLabelKey,
						srvv1alpha1.NoLinstorResourceLabelValue,
						pv.Labels)
				}
			},
		},
		{
			name:    "pv without linstor resource but already labeled - idempotent",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSIAndLabel()},
			pvMap:   buildPVMap(pvWithReplicatedCSIAndLabel()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{},
			},
			check: func(t *testing.T, m *Migrator) {
				pv := getPV(t, m)
				if pv.Labels[srvv1alpha1.NoLinstorResourceLabelKey] != srvv1alpha1.NoLinstorResourceLabelValue {
					t.Fatalf("label should remain %q, got labels=%v",
						srvv1alpha1.NoLinstorResourceLabelValue,
						pv.Labels)
				}
			},
		},
		{
			name:    "pv with linstor resource and stale label - label removed",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSIAndLabel()},
			pvMap:   buildPVMap(pvWithReplicatedCSIAndLabel()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{
					"pv-1": {{}}, // key exists and has at least one resource entry
				},
			},
			check: func(t *testing.T, m *Migrator) {
				pv := getPV(t, m)
				if _, ok := pv.Labels[srvv1alpha1.NoLinstorResourceLabelKey]; ok {
					t.Fatalf("label should be removed, got labels=%v", pv.Labels)
				}
			},
		},
		{
			name:    "pv with linstor resource and no label - unchanged",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSI()},
			pvMap:   buildPVMap(pvWithReplicatedCSI()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{
					"pv-1": {{}},
				},
			},
			check: func(t *testing.T, m *Migrator) {
				pv := getPV(t, m)
				if _, ok := pv.Labels[srvv1alpha1.NoLinstorResourceLabelKey]; ok {
					t.Fatalf("label should NOT be present, got labels=%v", pv.Labels)
				}
			},
		},
		{
			name:    "pv not found in cluster returns no error",
			objects: nil,
			pvMap:   buildPVMap(pvWithReplicatedCSI()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{},
			},
			check: func(_ *testing.T, _ *Migrator) {
				// No PV in the fake client — nothing to assert, just ensure no panic.
			},
		},
		{
			name:    "label with wrong value is corrected to true",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSIAndWrongLabel()},
			pvMap:   buildPVMap(pvWithReplicatedCSIAndWrongLabel()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{},
			},
			check: func(t *testing.T, m *Migrator) {
				pv := getPV(t, m)
				if pv.Labels[srvv1alpha1.NoLinstorResourceLabelKey] != srvv1alpha1.NoLinstorResourceLabelValue {
					t.Fatalf("expected label %q to be %q, got labels=%v",
						srvv1alpha1.NoLinstorResourceLabelKey,
						srvv1alpha1.NoLinstorResourceLabelValue,
						pv.Labels)
				}
			},
		},
		{
			name:    "patch error is wrapped and returned",
			objects: []*corev1.PersistentVolume{pvWithReplicatedCSI()},
			pvMap:   buildPVMap(pvWithReplicatedCSI()),
			db: &linstordb.LinstorDB{
				Resources: map[string][]srvlinstor.Resources{},
			},
			patchErr: errors.New("simulated patch failure"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Convert []*corev1.PersistentVolume to []kubecl.Object for newMigratorWithObjects.
			objs := make([]kubecl.Object, len(tt.objects))
			for i, obj := range tt.objects {
				objs[i] = obj
			}
			m := newMigratorWithObjects(t, objs...)

			// Inject a Patch error when the case requests it.
			if tt.patchErr != nil {
				m.client = newPatchingErrClient(t, m.client, tt.patchErr)
			}

			err := m.ensurePVNoLinstorResourceLabels(context.Background(), tt.db, tt.pvMap)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

// buildPVMap creates a pvMap from one or more PVs keyed by lowercased name, matching the
// production construction in stage1.go (pvMap[strings.ToLower(pv.Name)]).
func buildPVMap(pvs ...*corev1.PersistentVolume) map[string]corev1.PersistentVolume {
	m := make(map[string]corev1.PersistentVolume)
	for _, pv := range pvs {
		m[strings.ToLower(pv.Name)] = *pv
	}
	return m
}

// getPV fetches the "pv-1" PersistentVolume from the fake client and fails the test on error.
func getPV(t *testing.T, m *Migrator) *corev1.PersistentVolume {
	t.Helper()
	pv := &corev1.PersistentVolume{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pv-1"}, pv); err != nil {
		t.Fatalf("get PersistentVolume %q: %v", "pv-1", err)
	}
	return pv
}

// newPatchingErrClient wraps the given client so every Patch call returns the provided error.
// Get and other operations delegate to the underlying client, so the PV objects pre-populated on
// the base fake client remain visible. Used to exercise the error branch of
// ensurePVNoLinstorResourceLabels: a non-NotFound Patch error must be wrapped and returned.
func newPatchingErrClient(t *testing.T, base kubecl.Client, patchErr error) kubecl.Client {
	t.Helper()

	ww, ok := base.(kubecl.WithWatch)
	if !ok {
		t.Fatalf("base client %T does not implement client.WithWatch", base)
	}
	return interceptor.NewClient(ww, interceptor.Funcs{
		Patch: func(_ context.Context, _ kubecl.WithWatch, _ kubecl.Object, _ kubecl.Patch, _ ...kubecl.PatchOption) error {
			return patchErr
		},
	})
}
