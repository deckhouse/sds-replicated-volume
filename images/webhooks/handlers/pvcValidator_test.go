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

package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/slok/kubewebhook/v2/pkg/model"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_PVCValidate(t *testing.T) {
	replicatedProvisioner := "replicated.csi.storage.deckhouse.io"
	otherProvisioner := "other.csi.storage.deckhouse.io"

	tests := []struct {
		name           string
		obj            metav1.Object
		storageClasses []*storagev1.StorageClass
		username       string
		groups         []string
		envVars        map[string]string
		wantValid      bool
		wantMessage    string
		wantErr        bool
	}{
		{
			name: "Non-RWX PVC allowed",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC with other provisioner allowed",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: otherProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC with replicated provisioner and default allowed user",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username:  "system:serviceaccount:d8-virtualization:cdi-sa",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC with replicated provisioner and csi service account",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username:  "system:serviceaccount:d8-sds-replicated-volume:csi",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC with replicated provisioner and disallowed user",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username:    "system:serviceaccount:other:user",
			groups:      []string{},
			wantValid:   false,
			wantMessage: "Creating PVC with accessMode ReadWriteMany using StorageClass \"sc\" (provisioner \"replicated.csi.storage.deckhouse.io\") is not allowed for user \"system:serviceaccount:other:user\".",
		},
		{
			name: "RWX PVC with replicated provisioner and custom allowed user",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username: "custom-user",
			groups:   []string{},
			envVars: map[string]string{
				"PVC_RWX_ALLOWED_USERNAMES": "custom-user,other-user",
			},
			wantValid: true,
		},
		{
			name: "RWX PVC with replicated provisioner and custom allowed group",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "sc"}, Provisioner: replicatedProvisioner},
			},
			username: "user",
			groups:   []string{"custom-group"},
			envVars: map[string]string{
				"PVC_RWX_ALLOWED_GROUPS": "custom-group",
			},
			wantValid: true,
		},
		{
			name: "RWX PVC with no StorageClass - allowed (fail-open)",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("sc-not-found"),
				},
			},
			storageClasses: []*storagev1.StorageClass{},
			username:       "user",
			groups:         []string{},
			wantValid:      true,
		},
		{
			name: "RWX PVC naming the replicated class in the beta annotation and another class in spec denied",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc", Annotations: map[string]string{v1.BetaStorageClassAnnotation: "replicated"}},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("other"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "replicated"}, Provisioner: replicatedProvisioner},
				{ObjectMeta: metav1.ObjectMeta{Name: "other"}, Provisioner: otherProvisioner},
			},
			username:    "user",
			groups:      []string{},
			wantValid:   false,
			wantMessage: "Creating PVC with accessMode ReadWriteMany using StorageClass \"replicated\" (provisioner \"replicated.csi.storage.deckhouse.io\") is not allowed for user \"user\".",
		},
		{
			name: "RWX PVC with an empty beta annotation and the replicated class in spec allowed",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc", Annotations: map[string]string{v1.BetaStorageClassAnnotation: ""}},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr("replicated"),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "replicated"}, Provisioner: replicatedProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC with an explicitly empty class allowed while the replicated class is the default",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
					StorageClassName: stringPtr(""),
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "replicated", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}, Provisioner: replicatedProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC without a class allowed when the newest default class is not replicated",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "a-replicated", CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}, Provisioner: replicatedProvisioner},
				{ObjectMeta: metav1.ObjectMeta{Name: "b-other", CreationTimestamp: metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)), Annotations: map[string]string{"storageclass.beta.kubernetes.io/is-default-class": "true"}}, Provisioner: otherProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true,
		},
		{
			name: "RWX PVC without a class denied when the newest default class is replicated",
			obj: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc"},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
				},
			},
			storageClasses: []*storagev1.StorageClass{
				{ObjectMeta: metav1.ObjectMeta{Name: "a-other", CreationTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}, Provisioner: otherProvisioner},
				{ObjectMeta: metav1.ObjectMeta{Name: "b-replicated", CreationTimestamp: metav1.NewTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)), Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}, Provisioner: replicatedProvisioner},
			},
			username:  "user",
			groups:    []string{},
			wantValid: false,
		},
		{
			name: "Non-PVC object",
			obj: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod"},
			},
			username:  "user",
			groups:    []string{},
			wantValid: true, // Non-PVC objects return empty result which is treated as valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}
			defer func() {
				os.Unsetenv("PVC_RWX_ALLOWED_USERNAMES")
				os.Unsetenv("PVC_RWX_ALLOWED_GROUPS")
			}()

			// Setup fake client
			scheme := runtime.NewScheme()
			if err := storagev1.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add storagev1 to scheme: %v", err)
			}
			if err := v1.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add v1 to scheme: %v", err)
			}

			objs := make([]client.Object, 0, len(tt.storageClasses))
			for _, sc := range tt.storageClasses {
				objs = append(objs, sc)
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			// Mock kubeClientFactory to return fake client
			originalFactory := kubeClientFactory
			kubeClientFactory = func(_ string) (client.Client, error) {
				return cl, nil
			}
			defer func() {
				kubeClientFactory = originalFactory
			}()

			arReview := &model.AdmissionReview{
				UserInfo: authenticationv1.UserInfo{
					Username: tt.username,
					Groups:   tt.groups,
				},
			}

			result, err := PVCValidate(context.Background(), arReview, tt.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("PVCValidate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if result.Valid != tt.wantValid {
				t.Errorf("PVCValidate() Valid = %v, want %v", result.Valid, tt.wantValid)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Errorf("PVCValidate() Message = %v, want %v", result.Message, tt.wantMessage)
			}
		})
	}
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
