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

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/slok/kubewebhook/v2/pkg/model"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const testNamespace = "ns"

func snapshotSourceObjects() []client.Object {
	replicated := func(name string) *storagev1.StorageClass {
		return &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: name},
			Provisioner: replicatedCSIProvisioner,
			Parameters:  map[string]string{srv.CSIParamRSCNameKey: name},
		}
	}
	claim := func(name, class string) *v1.PersistentVolumeClaim {
		return &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec:       v1.PersistentVolumeClaimSpec{StorageClassName: stringPtr(class)},
		}
	}
	legacyRSC := func(name, pool string) *srv.ReplicatedStorageClass {
		return &srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       srv.ReplicatedStorageClassSpec{StoragePool: pool}, //nolint:staticcheck // SA1019: the LINSTOR control plane names its pool in the deprecated field
		}
	}
	newRSC := func(name string, poolType srv.ReplicatedStoragePoolType) *srv.ReplicatedStorageClass {
		return &srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: srv.ReplicatedStorageClassSpec{
				Storage: &srv.ReplicatedStorageClassStorage{Type: poolType},
			},
		}
	}

	return []client.Object{
		replicated("legacy-thick"),
		replicated("legacy-thin"),
		replicated("new-thick"),
		replicated("new-thin"),
		replicated("no-rsc"),
		replicated("missing-pool"),
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Provisioner: "other.csi.example.com"},
		legacyRSC("legacy-thick", "thick-pool"),
		legacyRSC("legacy-thin", "thin-pool"),
		legacyRSC("missing-pool", "no-such-pool"),
		newRSC("new-thick", srv.ReplicatedStoragePoolTypeLVM),
		newRSC("new-thin", srv.ReplicatedStoragePoolTypeLVMThin),
		&srv.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "thick-pool"},
			Spec:       srv.ReplicatedStoragePoolSpec{Type: srv.ReplicatedStoragePoolTypeLVM},
		},
		&srv.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "thin-pool"},
			Spec:       srv.ReplicatedStoragePoolSpec{Type: srv.ReplicatedStoragePoolTypeLVMThin},
		},
		claim("pvc-legacy-thick", "legacy-thick"),
		claim("pvc-legacy-thin", "legacy-thin"),
		claim("pvc-new-thick", "new-thick"),
		claim("pvc-new-thin", "new-thin"),
		claim("pvc-other", "other"),
		claim("pvc-no-rsc", "no-rsc"),
		&v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-no-class", Namespace: testNamespace}},
	}
}

func useFakeClient(t *testing.T, funcs interceptor.Funcs) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{storagev1.AddToScheme, v1.AddToScheme, srv.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("add to scheme: %v", err)
		}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(snapshotSourceObjects()...).WithInterceptorFuncs(funcs).Build()

	original := kubeClientFactory
	kubeClientFactory = func(_ string) (client.Client, error) { return cl, nil }
	t.Cleanup(func() { kubeClientFactory = original })
}

func clonePVC(class, sourceName string) *v1.PersistentVolumeClaim {
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: testNamespace},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: stringPtr(class),
			DataSource:       &v1.TypedLocalObjectReference{Kind: "PersistentVolumeClaim", Name: sourceName},
		},
	}
}

func restorePVC(class string) *v1.PersistentVolumeClaim {
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: testNamespace},
		Spec: v1.PersistentVolumeClaimSpec{
			StorageClassName: stringPtr(class),
			DataSourceRef: &v1.TypedObjectReference{
				APIGroup: stringPtr("snapshot.storage.k8s.io"),
				Kind:     "VolumeSnapshot",
				Name:     "snap",
			},
		},
	}
}

func Test_PVCDataSourceValidate(t *testing.T) {
	tests := []struct {
		name        string
		obj         metav1.Object
		wantValid   bool
		wantMessage []string
	}{
		{
			name:      "claim without a data source",
			obj:       &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: testNamespace}, Spec: v1.PersistentVolumeClaimSpec{StorageClassName: stringPtr("legacy-thick")}},
			wantValid: true,
		},
		{
			name:        "clone into a class of a thick LINSTOR pool",
			obj:         clonePVC("legacy-thick", "pvc-legacy-thin"),
			wantMessage: []string{`StorageClass "legacy-thick"`, `ReplicatedStoragePool "thick-pool"`, "LVMThin"},
		},
		{
			name:        "restore into a class of a thick LINSTOR pool",
			obj:         restorePVC("legacy-thick"),
			wantMessage: []string{`StorageClass "legacy-thick"`, `ReplicatedStoragePool "thick-pool"`, "restoring one from a VolumeSnapshot"},
		},
		{
			name: "restore through dataSource into a class of thick volumes of the new control plane",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: testNamespace},
				Spec: v1.PersistentVolumeClaimSpec{
					StorageClassName: stringPtr("new-thick"),
					DataSource:       &v1.TypedLocalObjectReference{APIGroup: stringPtr("snapshot.storage.k8s.io"), Kind: "VolumeSnapshot", Name: "snap"},
				},
			},
			wantMessage: []string{`StorageClass "new-thick"`, "spec.storage.type", "LVMThin"},
		},
		{
			name:        "clone of a claim of a thick pool into a thin class",
			obj:         clonePVC("legacy-thin", "pvc-legacy-thick"),
			wantMessage: []string{`PersistentVolumeClaim "pvc-legacy-thick"`, `StorageClass "legacy-thick"`, "cloning"},
		},
		{
			name:      "clone within a thin LINSTOR pool",
			obj:       clonePVC("legacy-thin", "pvc-legacy-thin"),
			wantValid: true,
		},
		{
			name:      "restore into a class of thin volumes of the new control plane",
			obj:       restorePVC("new-thin"),
			wantValid: true,
		},
		{
			name:      "clone into a class of another provisioner",
			obj:       clonePVC("other", "pvc-other"),
			wantValid: true,
		},
		{
			name:      "clone into a replicated class without a ReplicatedStorageClass",
			obj:       clonePVC("no-rsc", "pvc-no-rsc"),
			wantValid: true,
		},
		{
			name:      "restore into a class whose pool does not exist",
			obj:       restorePVC("missing-pool"),
			wantValid: true,
		},
		{
			name:      "clone into a class that does not exist",
			obj:       clonePVC("absent", "pvc-legacy-thin"),
			wantValid: true,
		},
		{
			name:      "clone of a claim that does not exist",
			obj:       clonePVC("legacy-thin", "absent"),
			wantValid: true,
		},
		{
			name:      "clone of a claim without a class",
			obj:       clonePVC("legacy-thin", "pvc-no-class"),
			wantValid: true,
		},
		{
			name: "data source of another kind",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: testNamespace},
				Spec: v1.PersistentVolumeClaimSpec{
					StorageClassName: stringPtr("legacy-thick"),
					DataSourceRef:    &v1.TypedObjectReference{APIGroup: stringPtr("cdi.kubevirt.io"), Kind: "VolumeImportSource", Name: "src"},
				},
			},
			wantValid: true,
		},
		{
			name:      "not a claim",
			obj:       &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod"}},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFakeClient(t, interceptor.Funcs{})

			result, err := PVCDataSourceValidate(context.Background(), &model.AdmissionReview{Namespace: testNamespace}, tt.obj)
			if err != nil {
				t.Fatalf("PVCDataSourceValidate() error = %v", err)
			}
			checkResult(t, result.Valid, result.Message, tt.wantValid, tt.wantMessage)
		})
	}
}

func volumeSnapshot(claimName string) *unstructured.Unstructured {
	vs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata":   map[string]any{"name": "snap"},
		"spec":       map[string]any{"source": map[string]any{}},
	}}
	if claimName != "" {
		_ = unstructured.SetNestedField(vs.Object, claimName, "spec", "source", "persistentVolumeClaimName")
	}
	return vs
}

func Test_VolumeSnapshotValidate(t *testing.T) {
	fromContent := volumeSnapshot("")
	_ = unstructured.SetNestedField(fromContent.Object, "content", "spec", "source", "volumeSnapshotContentName")

	tests := []struct {
		name        string
		obj         metav1.Object
		wantValid   bool
		wantMessage []string
	}{
		{
			name:        "claim of a thick LINSTOR pool",
			obj:         volumeSnapshot("pvc-legacy-thick"),
			wantMessage: []string{`PersistentVolumeClaim "pvc-legacy-thick"`, `StorageClass "legacy-thick"`, `ReplicatedStoragePool "thick-pool"`, "snapshots need a storage pool of type LVMThin"},
		},
		{
			name:        "claim of thick volumes of the new control plane",
			obj:         volumeSnapshot("pvc-new-thick"),
			wantMessage: []string{`PersistentVolumeClaim "pvc-new-thick"`, "spec.storage.type", "LVMThin"},
		},
		{
			name:      "claim of a thin LINSTOR pool",
			obj:       volumeSnapshot("pvc-legacy-thin"),
			wantValid: true,
		},
		{
			name:      "claim of thin volumes of the new control plane",
			obj:       volumeSnapshot("pvc-new-thin"),
			wantValid: true,
		},
		{
			name:      "claim of another provisioner",
			obj:       volumeSnapshot("pvc-other"),
			wantValid: true,
		},
		{
			name:      "claim of a replicated class without a ReplicatedStorageClass",
			obj:       volumeSnapshot("pvc-no-rsc"),
			wantValid: true,
		},
		{
			name:      "claim that does not exist",
			obj:       volumeSnapshot("absent"),
			wantValid: true,
		},
		{
			name:      "claim without a class",
			obj:       volumeSnapshot("pvc-no-class"),
			wantValid: true,
		},
		{
			name:      "pre-provisioned snapshot",
			obj:       fromContent,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFakeClient(t, interceptor.Funcs{})

			result, err := VolumeSnapshotValidate(context.Background(), &model.AdmissionReview{Namespace: testNamespace}, tt.obj)
			if err != nil {
				t.Fatalf("VolumeSnapshotValidate() error = %v", err)
			}
			checkResult(t, result.Valid, result.Message, tt.wantValid, tt.wantMessage)
		})
	}
}

func Test_SnapshotSourceValidate_ReportsLookupErrors(t *testing.T) {
	boom := errors.New("apiserver unavailable")
	useFakeClient(t, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*srv.ReplicatedStorageClass); ok {
				return boom
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	if _, err := PVCDataSourceValidate(context.Background(), &model.AdmissionReview{}, restorePVC("legacy-thick")); !errors.Is(err, boom) {
		t.Errorf("PVCDataSourceValidate() error = %v, want %v", err, boom)
	}
	if _, err := VolumeSnapshotValidate(context.Background(), &model.AdmissionReview{Namespace: testNamespace}, volumeSnapshot("pvc-legacy-thick")); !errors.Is(err, boom) {
		t.Errorf("VolumeSnapshotValidate() error = %v, want %v", err, boom)
	}
}

func checkResult(t *testing.T, valid bool, message string, wantValid bool, wantMessage []string) {
	t.Helper()
	if valid != wantValid {
		t.Fatalf("Valid = %v, want %v (message %q)", valid, wantValid, message)
	}
	for _, part := range wantMessage {
		if !strings.Contains(message, part) {
			t.Errorf("Message %q does not contain %q", message, part)
		}
	}
}
