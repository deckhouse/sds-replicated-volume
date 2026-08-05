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
	"log/slog"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/config"
)

func TestEnsureConfigMap(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	tests := []struct {
		name       string
		getErrs    []error
		createErrs []error
		objects    []kubecl.Object
		wantState  string
		wantErr    bool
	}{
		{
			name: "configmap_exists_with_state",
			objects: []kubecl.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.MigrationConfigMapName,
						Namespace: config.ModuleNamespace,
					},
					Data: map[string]string{
						"state": "stage2-completed",
					},
				},
			},
			wantState: "stage2-completed",
		},
		{
			name: "configmap_exists_empty_state",
			objects: []kubecl.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.MigrationConfigMapName,
						Namespace: config.ModuleNamespace,
					},
				},
			},
			wantState: config.StateNotStarted,
		},
		{
			name:      "configmap_not_found_then_created",
			wantState: config.StateNotStarted,
		},
		{
			// Simulate a race: Get returned NotFound (cache lag or first run),
			// Create returns AlreadyExists because another instance created the
			// ConfigMap concurrently with an advanced migration state. The function
			// must re-Get and recover the real state, NOT assume StateNotStarted.
			name: "configmap_race_already_exists_recovers_real_state",
			getErrs: []error{
				apierrors.NewNotFound(
					schema.GroupResource{Group: "", Resource: "configmaps"},
					config.MigrationConfigMapName,
				),
			},
			createErrs: []error{
				apierrors.NewAlreadyExists(
					schema.GroupResource{Group: "", Resource: "configmaps"},
					config.MigrationConfigMapName,
				),
			},
			objects: []kubecl.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.MigrationConfigMapName,
						Namespace: config.ModuleNamespace,
					},
					Data: map[string]string{
						"state": "stage2-completed",
					},
				},
			},
			wantState: "stage2-completed",
		},
		{
			name: "get_returns_transient_error_then_success",
			getErrs: []error{
				apierrors.NewServiceUnavailable("unavailable"),
			},
			objects: []kubecl.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.MigrationConfigMapName,
						Namespace: config.ModuleNamespace,
					},
					Data: map[string]string{
						"state": "stage1-completed",
					},
				},
			},
			wantState: "stage1-completed",
		},
		{
			name: "get_returns_permanent_error",
			getErrs: []error{
				apierrors.NewForbidden(
					schema.GroupResource{Group: "", Resource: "configmaps"},
					config.MigrationConfigMapName,
					errors.New("forbidden"),
				),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tt.objects) > 0 {
				builder = builder.WithObjects(tt.objects...)
			}
			fakeClient := builder.Build()

			wrapped := &errTrackingClient{
				Client:     fakeClient,
				getErrs:    tt.getErrs,
				createErrs: tt.createErrs,
			}

			m := &Migrator{
				client:       wrapped,
				log:          slog.Default(),
				retryBackoff: wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Jitter: 0, Cap: time.Millisecond},
			}

			state, err := m.ensureConfigMap(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if state != tt.wantState {
				t.Fatalf("state = %q, want %q", state, tt.wantState)
			}
		})
	}
}
