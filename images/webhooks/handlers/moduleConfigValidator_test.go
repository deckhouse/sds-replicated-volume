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
	"strings"
	"testing"

	"github.com/slok/kubewebhook/v2/pkg/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
)

const testNamespace = "d8-sds-replicated-volume"

func TestModuleConfigValidate(t *testing.T) {
	t.Setenv("POD_NAMESPACE", testNamespace)

	tests := []struct {
		name               string
		obj                metav1.Object
		existingModuleConfig *d8commonapi.ModuleConfig
		configMap          *corev1.ConfigMap
		wantValid          bool
		wantMessage        string
		wantMessageContains string
	}{
		{
			name:      "Non ModuleConfig object allowed",
			obj:       &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod"}},
			wantValid: true,
		},
		{
			name:      "Other ModuleConfig ignored",
			obj:       &d8commonapi.ModuleConfig{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
			wantValid: true,
		},
		{
			name: "No drbdVersion in settings",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{}},
			},
			wantValid: true,
		},
		{
			name: "drbdVersion is not a string",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": 10}},
			},
			wantValid:   false,
			wantMessage: "drbdVersion must be a string",
		},
		{
			name: "Missing ConfigMap",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": "9.2.13"}},
			},
			wantValid:          false,
			wantMessageContains: "failed to read",
		},
		{
			name: "Disallowed version",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": "9.2.13"}},
			},
			configMap: allowedVersionsConfigMap("9.2.16"),
			wantValid: false,
			wantMessage: "drbdVersion \"9.2.13\" is not allowed",
		},
		{
			name: "Downgrade is blocked",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": "9.2.13"}},
			},
			existingModuleConfig: moduleConfigWithVersion("9.2.16"),
			configMap:            allowedVersionsConfigMap("9.2.13", "9.2.16"),
			wantValid:            false,
			wantMessage:          "drbdVersion downgrade is not allowed (current 9.2.16, requested 9.2.13)",
		},
		{
			name: "Upgrade is allowed",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": "9.2.16"}},
			},
			existingModuleConfig: moduleConfigWithVersion("9.2.13"),
			configMap:            allowedVersionsConfigMap("9.2.13", "9.2.16"),
			wantValid:            true,
		},
		{
			name: "Allowed when no current ModuleConfig",
			obj: &d8commonapi.ModuleConfig{
				ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
				Spec:       d8commonapi.ModuleConfigSpec{Settings: map[string]interface{}{"drbdVersion": "9.2.16"}},
			},
			configMap: allowedVersionsConfigMap("9.2.16"),
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]client.Object, 0, 2)
			if tt.existingModuleConfig != nil {
				objs = append(objs, tt.existingModuleConfig)
			}
			if tt.configMap != nil {
				objs = append(objs, tt.configMap)
			}

			cl := newFakeClient(t, objs...)
			originalFactory := kubeClientFactory
			kubeClientFactory = func(_ string) (client.Client, error) {
				return cl, nil
			}
			defer func() {
				kubeClientFactory = originalFactory
			}()

			result, err := ModuleConfigValidate(context.Background(), &model.AdmissionReview{}, tt.obj)
			if err != nil {
				t.Fatalf("ModuleConfigValidate() unexpected error: %v", err)
			}
			if result.Valid != tt.wantValid {
				t.Fatalf("ModuleConfigValidate() Valid = %v, want %v", result.Valid, tt.wantValid)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Fatalf("ModuleConfigValidate() Message = %q, want %q", result.Message, tt.wantMessage)
			}
			if tt.wantMessageContains != "" && !strings.Contains(result.Message, tt.wantMessageContains) {
				t.Fatalf("ModuleConfigValidate() Message = %q, want to contain %q", result.Message, tt.wantMessageContains)
			}
		})
	}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 scheme: %v", err)
	}
	if err := d8commonapi.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add d8commonapi scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func allowedVersionsConfigMap(versions ...string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      drbdVersionsConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			drbdVersionsConfigMapKey: strings.Join(versions, "\n"),
		},
	}
}

func moduleConfigWithVersion(version string) *d8commonapi.ModuleConfig {
	return &d8commonapi.ModuleConfig{
		ObjectMeta: metav1.ObjectMeta{Name: sdsReplicatedVolumeModuleName},
		Spec: d8commonapi.ModuleConfigSpec{
			Settings: map[string]interface{}{
				"drbdVersion": version,
			},
		},
	}
}
