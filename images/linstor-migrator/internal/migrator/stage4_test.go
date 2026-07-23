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
	"log/slog"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	srvv1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

// --- test helpers -----------------------------------------------------------

func rvWithSwitchLabel(name string) *srvv1alpha1.ReplicatedVolume {
	return &srvv1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				srvv1alpha1.SwitchToAutoConfigurationLabelKey: srvv1alpha1.SwitchToAutoConfigurationLabelValue,
			},
		},
		Spec: srvv1alpha1.ReplicatedVolumeSpec{
			ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
			ManualConfiguration: &srvv1alpha1.ReplicatedVolumeConfiguration{
				ReplicatedStoragePoolName: "linstor-auto-fast",
			},
		},
	}
}

func rvManual(name string) *srvv1alpha1.ReplicatedVolume {
	return &srvv1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srvv1alpha1.ReplicatedVolumeSpec{
			ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
			ManualConfiguration: &srvv1alpha1.ReplicatedVolumeConfiguration{
				ReplicatedStoragePoolName: "linstor-auto-fast",
			},
		},
	}
}

func pvWithStorageClass(scName string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pvc-1",
		},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName: scName,
		},
	}
}

func rscReady(name string) *srvv1alpha1.ReplicatedStorageClass {
	return &srvv1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: srvv1alpha1.ReplicatedStorageClassStatus{
			Phase: srvv1alpha1.ReplicatedStorageClassPhaseReady,
		},
	}
}

func rscWaiting(name string) *srvv1alpha1.ReplicatedStorageClass {
	return &srvv1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: srvv1alpha1.ReplicatedStorageClassStatus{
			Phase: srvv1alpha1.ReplicatedStorageClassPhaseWaitingForStoragePool,
		},
	}
}

// rscWithPhase creates a ReplicatedStorageClass with an arbitrary phase for testing
// terminal vs non-terminal phase handling.
func rscWithPhase(name string, phase srvv1alpha1.ReplicatedStorageClassPhase) *srvv1alpha1.ReplicatedStorageClass {
	return &srvv1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: srvv1alpha1.ReplicatedStorageClassStatus{
			Phase: phase,
		},
	}
}

func rscWithStoragePool(name, poolName string) *srvv1alpha1.ReplicatedStorageClass {
	return &srvv1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srvv1alpha1.ReplicatedStorageClassSpec{
			StoragePool: poolName,
		},
		Status: srvv1alpha1.ReplicatedStorageClassStatus{
			Phase: srvv1alpha1.ReplicatedStorageClassPhaseReady,
		},
	}
}

func rscReadyNoPool(name string) *srvv1alpha1.ReplicatedStorageClass {
	return &srvv1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srvv1alpha1.ReplicatedStorageClassSpec{
			StoragePool: "",
		},
		Status: srvv1alpha1.ReplicatedStorageClassStatus{
			Phase: srvv1alpha1.ReplicatedStorageClassPhaseReady,
		},
	}
}

func sourceRSP(name string) *srvv1alpha1.ReplicatedStoragePool {
	return &srvv1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srvv1alpha1.ReplicatedStoragePoolSpec{
			Type: srvv1alpha1.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "vg-1"},
			},
			SystemNetworkNames: []string{"Internal"},
		},
	}
}

// labeledRSP creates an RSP owned by the migrator (with the migration conversion label).
func labeledRSP(name string) *srvv1alpha1.ReplicatedStoragePool {
	return &srvv1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey: srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue,
			},
		},
		Spec: srvv1alpha1.ReplicatedStoragePoolSpec{
			Type: srvv1alpha1.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "vg-1"},
			},
			SystemNetworkNames: []string{"Internal"},
		},
	}
}

// foreignRSP creates an RSP NOT owned by the migrator (no migration label).
func foreignRSP(name string) *srvv1alpha1.ReplicatedStoragePool {
	return &srvv1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: srvv1alpha1.ReplicatedStoragePoolSpec{
			Type: srvv1alpha1.ReplicatedStoragePoolTypeLVMThin,
			LVMVolumeGroups: []srvv1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "vg-foreign", ThinPoolName: "thin"},
			},
			SystemNetworkNames: []string{"Internal"},
		},
	}
}

func migrationConfigMap(state string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.MigrationConfigMapName,
			Namespace: config.ModuleNamespace,
		},
		Data: map[string]string{"state": state},
	}
}

func newMigratorWithObjects(t *testing.T, objects ...kubecl.Object) *Migrator {
	t.Helper()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = srvv1alpha1.AddToScheme(scheme)

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	return &Migrator{
		client:              cl,
		log:                 slog.Default(),
		retryBackoff:        wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Jitter: 0, Cap: time.Millisecond},
		stage4MaxIterations: DefaultStage4MaxIterations,
		stage4PollInterval:  DefaultStage4PollInterval,
	}
}

// migrationLabel checks the conversion label on a stored RSP.
func assertLabeledRSP(t *testing.T, m *Migrator, name string) {
	t.Helper()
	rsp := &srvv1alpha1.ReplicatedStoragePool{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: name}, rsp); err != nil {
		t.Fatalf("expected RSP %q to exist: %v", name, err)
	}
	if rsp.Labels[srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey] != srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue {
		t.Fatalf("RSP %q missing migration conversion label, labels=%v", name, rsp.Labels)
	}
}

// assertRSPNotFound checks that an RSP does not exist.
func assertRSPNotFound(t *testing.T, m *Migrator, name string) {
	t.Helper()
	rsp := &srvv1alpha1.ReplicatedStoragePool{}
	err := m.client.Get(context.Background(), types.NamespacedName{Name: name}, rsp)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected RSP %q to be deleted, got err=%v", name, err)
	}
}

// assertRSPExists checks that an RSP exists.
func assertRSPExists(t *testing.T, m *Migrator, name string) *srvv1alpha1.ReplicatedStoragePool {
	t.Helper()
	rsp := &srvv1alpha1.ReplicatedStoragePool{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: name}, rsp); err != nil {
		t.Fatalf("expected RSP %q to exist: %v", name, err)
	}
	return rsp
}

// assertBlockedRV checks that a ReplicatedVolume has the auto-configuration-blocked label
// and does NOT have the switch-to-auto label. The ConfigurationMode must remain Manual.
func assertBlockedRV(t *testing.T, rv *srvv1alpha1.ReplicatedVolume) {
	t.Helper()
	if rv.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey] != srvv1alpha1.AutoConfigurationBlockedLabelValue {
		t.Fatal("auto-configuration-blocked label should be set")
	}
	if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
		t.Fatal("switch-to-auto label should be removed")
	}
	if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeManual {
		t.Fatalf("ConfigurationMode = %q, want Manual", rv.Spec.ConfigurationMode)
	}
}

// assertPendingRV checks that a ReplicatedVolume still has the switch-to-auto label,
// does NOT have the auto-configuration-blocked label, and remains in Manual mode.
func assertPendingRV(t *testing.T, rv *srvv1alpha1.ReplicatedVolume) {
	t.Helper()
	if rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey] != srvv1alpha1.SwitchToAutoConfigurationLabelValue {
		t.Fatal("switch-to-auto label should remain")
	}
	if _, ok := rv.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey]; ok {
		t.Fatal("auto-configuration-blocked label should NOT be set")
	}
	if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeManual {
		t.Fatalf("ConfigurationMode = %q, want Manual", rv.Spec.ConfigurationMode)
	}
}

// --- TestEnsureConversionRSPs -------------------------------------------------

func TestEnsureConversionRSPs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		objects            []kubecl.Object
		checkAfter         func(t *testing.T, m *Migrator)
		wantNonConvertible map[string]struct{} // expected RSC names in non-convertible set
		wantErr            bool
	}{
		{
			name: "creates_copy_from_linstor_auto",
			objects: []kubecl.Object{
				rscWithStoragePool("fast", "fast"),
				sourceRSP("linstor-auto-fast"),
			},
			checkAfter: func(t *testing.T, m *Migrator) {
				assertLabeledRSP(t, m, "fast")
			},
		},
		{
			name: "source_not_found_warning_skipped",
			objects: []kubecl.Object{
				rscWithStoragePool("unknown", "unknown"),
			},
			wantNonConvertible: map[string]struct{}{"unknown": {}},
			checkAfter: func(t *testing.T, m *Migrator) {
				assertRSPNotFound(t, m, "unknown")
			},
		},
		{
			name: "idempotent_target_exists_with_label",
			objects: []kubecl.Object{
				rscWithStoragePool("fast", "fast"),
				sourceRSP("linstor-auto-fast"),
				labeledRSP("fast"), // target RSP already owned by migrator
			},
			checkAfter: func(t *testing.T, m *Migrator) {
				rsp := assertRSPExists(t, m, "fast")
				// Label must be preserved; Spec must not be overwritten.
				if rsp.Labels[srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey] != srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue {
					t.Fatal("migration conversion label should remain")
				}
				if rsp.Spec.Type != srvv1alpha1.ReplicatedStoragePoolTypeLVM {
					t.Fatalf("Spec.Type should remain LVM, got %v", rsp.Spec.Type)
				}
			},
		},
		{
			name: "target_exists_foreign_without_label",
			objects: []kubecl.Object{
				rscWithStoragePool("fast", "fast"),
				sourceRSP("linstor-auto-fast"),
				foreignRSP("fast"), // pre-existing RSP without migration label
			},
			checkAfter: func(t *testing.T, m *Migrator) {
				rsp := assertRSPExists(t, m, "fast")
				// Foreign RSP: label must NOT be added, Spec must NOT be overwritten.
				if _, ok := rsp.Labels[srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey]; ok {
					t.Fatal("foreign RSP must not get migration label")
				}
				if rsp.Spec.Type != srvv1alpha1.ReplicatedStoragePoolTypeLVMThin {
					t.Fatalf("foreign RSP Spec.Type should remain LVMThin, got %v", rsp.Spec.Type)
				}
			},
		},
		{
			name: "rsc_without_storage_pool_skipped",
			objects: []kubecl.Object{
				rscReadyNoPool("nopool"),
				sourceRSP("linstor-auto-nopool"),
			},
			checkAfter: func(t *testing.T, m *Migrator) {
				// No RSP should be created — RSC has no StoragePool.
				assertRSPNotFound(t, m, "nopool")
			},
		},
		{
			name: "no_rsc_no_op",
			objects: []kubecl.Object{
				sourceRSP("linstor-auto-orphan"),
			},
			checkAfter: func(_ *testing.T, _ *Migrator) {
				// No RSC → nothing happens.
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			nonConvertible, err := m.ensureConversionRSPs(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNonConvertible != nil {
				if len(nonConvertible) != len(tt.wantNonConvertible) {
					t.Fatalf("nonConvertible length = %d, want %d", len(nonConvertible), len(tt.wantNonConvertible))
				}
				for k := range tt.wantNonConvertible {
					if _, ok := nonConvertible[k]; !ok {
						t.Fatalf("missing key %q in nonConvertible set", k)
					}
				}
			} else if len(nonConvertible) > 0 {
				t.Fatalf("nonConvertible should be empty, got %v", nonConvertible)
			}

			if tt.checkAfter != nil {
				tt.checkAfter(t, m)
			}
		})
	}
}

// --- TestSwitchRVsToAuto ----------------------------------------------------

func TestSwitchRVsToAuto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		objects            []kubecl.Object
		nonConvertibleRSCs map[string]struct{}
		wantPending        int
		checkRV            func(t *testing.T, m *Migrator)
	}{
		{
			name: "rsc_ready_patches_rv",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscReady("fast"),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeAuto {
					t.Fatalf("ConfigurationMode = %q, want Auto", rv.Spec.ConfigurationMode)
				}
				if rv.Spec.ReplicatedStorageClassName != "fast" {
					t.Fatalf("ReplicatedStorageClassName = %q, want fast", rv.Spec.ReplicatedStorageClassName)
				}
				if rv.Spec.ManualConfiguration != nil {
					t.Fatal("ManualConfiguration should be nil")
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
			},
		},
		{
			name: "rsc_not_ready_pending",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWaiting("fast"),
			},
			wantPending: 1,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeManual {
					t.Fatalf("ConfigurationMode = %q, want Manual", rv.Spec.ConfigurationMode)
				}
			},
		},
		{
			name: "rsc_not_found_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("missing"),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "empty_storageclassname_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass(""),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "rsc_terminal_phase_insufficient_nodes_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", srvv1alpha1.ReplicatedStorageClassPhaseInsufficientNodes),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "rsc_terminal_phase_invalid_configuration_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", srvv1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "rsc_terminal_phase_terminating_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", srvv1alpha1.ReplicatedStorageClassPhaseTerminating),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "rsc_rolling_out_pending",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", srvv1alpha1.ReplicatedStorageClassPhaseRollingOut),
			},
			wantPending: 1,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertPendingRV(t, rv)
			},
		},
		{
			name: "rsc_partially_aligned_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", srvv1alpha1.ReplicatedStorageClassPhasePartiallyAligned),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "waiting_for_storage_pool_non_convertible_marks_blocked",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("stuck"),
				rscWithPhase("stuck", srvv1alpha1.ReplicatedStorageClassPhaseWaitingForStoragePool),
			},
			nonConvertibleRSCs: map[string]struct{}{"stuck": {}},
			wantPending:        0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertBlockedRV(t, rv)
			},
		},
		{
			name: "pv_not_found_marks_orphaned",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
			},
			wantPending: 0,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
				if rv.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] != srvv1alpha1.NoPersistentVolumeLabelValue {
					t.Fatal("no-persistent-volume label should be set")
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeManual {
					t.Fatalf("ConfigurationMode = %q, want Manual", rv.Spec.ConfigurationMode)
				}
			},
		},
		{
			name: "rsc_empty_phase_pending",
			objects: []kubecl.Object{
				rvWithSwitchLabel("pvc-1"),
				pvWithStorageClass("fast"),
				rscWithPhase("fast", ""),
			},
			wantPending: 1,
			checkRV: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				assertPendingRV(t, rv)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			pending, err := m.switchRVsToAuto(context.Background(), tt.nonConvertibleRSCs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pending != tt.wantPending {
				t.Fatalf("pending = %d, want %d", pending, tt.wantPending)
			}
			if tt.checkRV != nil {
				tt.checkRV(t, m)
			}
		})
	}
}

// --- TestCleanupConversionRSPs ----------------------------------------------

func TestCleanupConversionRSPs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		objects      []kubecl.Object
		wantLeftover int
		wantDeleted  []string
		wantNotDel   []string
		wantErr      bool
	}{
		{
			name: "deletes_labeled_rsp_when_rsc_converted",
			objects: []kubecl.Object{
				labeledRSP("fast"),
				rscReadyNoPool("fast"), // StoragePool is "" → converted
			},
			wantLeftover: 0,
			wantDeleted:  []string{"fast"},
		},
		{
			name: "keeps_labeled_rsp_when_rsc_still_pending",
			objects: []kubecl.Object{
				labeledRSP("fast"),
				rscWithStoragePool("fast", "fast"), // still has StoragePool
			},
			wantLeftover: 1,
			wantNotDel:   []string{"fast"},
		},
		{
			name: "no_labeled_rsp_returns_zero",
			objects: []kubecl.Object{
				rscReadyNoPool("fast"),
			},
			wantLeftover: 0,
		},
		{
			name: "rsp_already_gone_swallows_notfound",
			objects: []kubecl.Object{
				rscReadyNoPool("fast"),
				// No labeled RSP in store → early exit.
			},
			wantLeftover: 0,
		},
		{
			name: "foreign_rsp_without_label_not_touched",
			objects: []kubecl.Object{
				foreignRSP("fast"),
				rscReadyNoPool("fast"), // converted
			},
			wantLeftover: 0,
			wantNotDel:   []string{"fast"}, // foreign RSP must survive
		},
		{
			name: "mixed_labeled_and_foreign",
			objects: []kubecl.Object{
				labeledRSP("owned"),
				foreignRSP("other"),
				rscReadyNoPool("owned"),
				rscReadyNoPool("other"),
			},
			wantLeftover: 0,
			wantDeleted:  []string{"owned"},
			wantNotDel:   []string{"other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			leftover, err := m.cleanupConversionRSPs(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if leftover != tt.wantLeftover {
				t.Fatalf("leftover = %d, want %d", leftover, tt.wantLeftover)
			}

			for _, name := range tt.wantDeleted {
				assertRSPNotFound(t, m, name)
			}
			for _, name := range tt.wantNotDel {
				assertRSPExists(t, m, name)
			}
		})
	}
}

// --- TestPatchRVToAuto -----------------------------------------------------

func TestPatchRVToAuto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		objects []kubecl.Object
		rvName  string
		rscName string
		wantErr bool
		check   func(t *testing.T, m *Migrator)
	}{
		{
			name: "success_switches_to_auto",
			objects: []kubecl.Object{
				rvWithSwitchLabel("test-rv"),
			},
			rvName:  "test-rv",
			rscName: "my-rsc",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeAuto {
					t.Fatalf("ConfigurationMode = %q, want Auto", rv.Spec.ConfigurationMode)
				}
				if rv.Spec.ReplicatedStorageClassName != "my-rsc" {
					t.Fatalf("ReplicatedStorageClassName = %q, want my-rsc", rv.Spec.ReplicatedStorageClassName)
				}
				if rv.Spec.ManualConfiguration != nil {
					t.Fatal("ManualConfiguration should be nil")
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
			},
		},
		{
			name:    "rv_not_found_returns_nil",
			objects: nil,
			rvName:  "nonexistent",
			rscName: "my-rsc",
			wantErr: false,
		},
		{
			name: "no_label_still_patches",
			objects: []kubecl.Object{
				rvManual("test-rv"),
			},
			rvName:  "test-rv",
			rscName: "my-rsc",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeAuto {
					t.Fatalf("ConfigurationMode = %q, want Auto", rv.Spec.ConfigurationMode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			err := m.patchRVToAuto(context.Background(),
				&srvv1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: tt.rvName}},
				tt.rscName,
			)
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

// --- TestPatchRVMarkOrphaned -----------------------------------------------

func TestPatchRVMarkOrphaned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		objects []kubecl.Object
		rvName  string
		wantErr bool
		check   func(t *testing.T, m *Migrator)
	}{
		{
			name: "marks_orphaned_removes_switch_adds_no_pv",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
						Labels: map[string]string{
							srvv1alpha1.SwitchToAutoConfigurationLabelKey: srvv1alpha1.SwitchToAutoConfigurationLabelValue,
							"other": "val",
						},
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
				if rv.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] != srvv1alpha1.NoPersistentVolumeLabelValue {
					t.Fatal("no-persistent-volume label should be set")
				}
				if rv.Labels["other"] != "val" {
					t.Fatal("other label should be preserved")
				}
			},
		},
		{
			name: "marks_orphaned_preserves_existing_no_pv",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
						Labels: map[string]string{
							srvv1alpha1.SwitchToAutoConfigurationLabelKey: srvv1alpha1.SwitchToAutoConfigurationLabelValue,
							srvv1alpha1.NoPersistentVolumeLabelKey:        srvv1alpha1.NoPersistentVolumeLabelValue,
						},
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
				if rv.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] != srvv1alpha1.NoPersistentVolumeLabelValue {
					t.Fatal("no-persistent-volume label should remain set")
				}
			},
		},
		{
			name: "idempotent_already_marked",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
						Labels: map[string]string{
							srvv1alpha1.NoPersistentVolumeLabelKey: srvv1alpha1.NoPersistentVolumeLabelValue,
						},
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] != srvv1alpha1.NoPersistentVolumeLabelValue {
					t.Fatal("no-persistent-volume label should remain set")
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should not appear")
				}
			},
		},
		{
			name: "nil_labels_adds_no_pv",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-rv",
						Labels: nil,
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Labels[srvv1alpha1.NoPersistentVolumeLabelKey] != srvv1alpha1.NoPersistentVolumeLabelValue {
					t.Fatal("no-persistent-volume label should be set")
				}
			},
		},
		{
			name:    "rv_not_found_returns_nil",
			objects: nil,
			rvName:  "nonexistent",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			err := m.patchRVMarkOrphaned(context.Background(),
				&srvv1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: tt.rvName}},
			)
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

// --- TestPatchRVBlockAutoConfiguration --------------------------------------

func TestPatchRVBlockAutoConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		objects []kubecl.Object
		rvName  string
		reason  string
		wantErr bool
		check   func(t *testing.T, m *Migrator)
	}{
		{
			name: "marks_blocked_removes_switch_label",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
						Labels: map[string]string{
							srvv1alpha1.SwitchToAutoConfigurationLabelKey: srvv1alpha1.SwitchToAutoConfigurationLabelValue,
							"other": "val",
						},
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			reason: "RSC broken",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey] != srvv1alpha1.AutoConfigurationBlockedLabelValue {
					t.Fatal("auto-configuration-blocked label should be set")
				}
				if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
					t.Fatal("switch-to-auto label should be removed")
				}
				if rv.Labels["other"] != "val" {
					t.Fatal("other label should be preserved")
				}
				if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeManual {
					t.Fatalf("ConfigurationMode = %q, want Manual", rv.Spec.ConfigurationMode)
				}
			},
		},
		{
			name: "idempotent_already_blocked",
			objects: []kubecl.Object{
				&srvv1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
						Labels: map[string]string{
							srvv1alpha1.AutoConfigurationBlockedLabelKey: srvv1alpha1.AutoConfigurationBlockedLabelValue,
						},
					},
					Spec: srvv1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: srvv1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
			rvName: "test-rv",
			reason: "still broken",
			check: func(t *testing.T, m *Migrator) {
				rv := &srvv1alpha1.ReplicatedVolume{}
				if err := m.client.Get(context.Background(), types.NamespacedName{Name: "test-rv"}, rv); err != nil {
					t.Fatalf("get RV: %v", err)
				}
				if rv.Labels[srvv1alpha1.AutoConfigurationBlockedLabelKey] != srvv1alpha1.AutoConfigurationBlockedLabelValue {
					t.Fatal("blocked label should remain")
				}
			},
		},
		{
			name:    "rv_not_found_returns_nil",
			objects: nil,
			rvName:  "nonexistent",
			reason:  "anything",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMigratorWithObjects(t, tt.objects...)

			err := m.patchRVBlockAutoConfiguration(context.Background(),
				&srvv1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: tt.rvName}},
				tt.reason,
			)
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

// --- TestRunStage4_HappyPath -------------------------------------------------

func TestRunStage4_HappyPath(t *testing.T) {
	// RSC without StoragePool → no conversion RSPs needed → runStage4 completes
	// in one iteration.
	objects := []kubecl.Object{
		rvWithSwitchLabel("pvc-1"),
		pvWithStorageClass("fast"),
		rscReadyNoPool("fast"),
		migrationConfigMap(config.StateStage3Completed),
	}

	m := newMigratorWithObjects(t, objects...)
	m.stage4PollInterval = time.Millisecond

	err := m.runStage4(context.Background())
	if err != nil {
		t.Fatalf("runStage4: %v", err)
	}

	// Verify ConfigMap state is all_completed.
	cm := &corev1.ConfigMap{}
	if err := m.client.Get(context.Background(), types.NamespacedName{
		Namespace: config.ModuleNamespace,
		Name:      config.MigrationConfigMapName,
	}, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data["state"] != config.StateAllCompleted {
		t.Fatalf("ConfigMap state = %q, want %q", cm.Data["state"], config.StateAllCompleted)
	}

	// Verify RV was switched to Auto.
	rv := &srvv1alpha1.ReplicatedVolume{}
	if err := m.client.Get(context.Background(), types.NamespacedName{Name: "pvc-1"}, rv); err != nil {
		t.Fatalf("get RV: %v", err)
	}
	if rv.Spec.ConfigurationMode != srvv1alpha1.ReplicatedVolumeConfigurationModeAuto {
		t.Fatalf("ConfigurationMode = %q, want Auto", rv.Spec.ConfigurationMode)
	}
	if rv.Spec.ReplicatedStorageClassName != "fast" {
		t.Fatalf("ReplicatedStorageClassName = %q, want fast", rv.Spec.ReplicatedStorageClassName)
	}
	if rv.Spec.ManualConfiguration != nil {
		t.Fatal("ManualConfiguration should be nil")
	}
	if _, ok := rv.Labels[srvv1alpha1.SwitchToAutoConfigurationLabelKey]; ok {
		t.Fatal("switch-to-auto label should be removed")
	}

	// Verify no labeled conversion RSPs left (cleanup ran successfully).
	var rspList srvv1alpha1.ReplicatedStoragePoolList
	_ = m.client.List(context.Background(), &rspList, kubecl.MatchingLabels{
		srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelKey: srvv1alpha1.MigrationConversionReplicatedStoragePoolLabelValue,
	})
	if len(rspList.Items) > 0 {
		t.Fatalf("expected no labeled conversion RSPs, found %d", len(rspList.Items))
	}
}

// --- TestRunStage4_ExceedsIterations -----------------------------------------

func TestRunStage4_ExceedsIterations(t *testing.T) {
	// RSC in WaitingForStoragePool → non-terminal, non-Ready → pending → exceeds iterations.
	// Spec.StoragePool="" avoids conversion RSP creation (ensureConversionRSPs skips it).
	objects := []kubecl.Object{
		rvWithSwitchLabel("pvc-1"),
		pvWithStorageClass("stuck"),
		rscWithPhase("stuck", srvv1alpha1.ReplicatedStorageClassPhaseWaitingForStoragePool),
		migrationConfigMap(config.StateStage3Completed),
	}

	m := newMigratorWithObjects(t, objects...)
	m.stage4MaxIterations = 1
	m.stage4PollInterval = time.Millisecond

	err := m.runStage4(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded 1 iterations") {
		t.Fatalf("error should mention exceeded iterations, got: %v", err)
	}
}
