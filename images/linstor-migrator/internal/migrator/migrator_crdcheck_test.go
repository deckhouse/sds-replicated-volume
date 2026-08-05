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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCrdExistsWithRetry(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	const crdName = "test-crd.example.com"

	tests := []struct {
		name      string
		getErrs   []error
		objects   []client.Object
		wantFound bool
		wantErr   bool
	}{
		{
			name: "crd_exists",
			objects: []client.Object{
				&apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{Name: crdName},
				},
			},
			wantFound: true,
			wantErr:   false,
		},
		{
			name:      "crd_not_found",
			wantFound: false,
			wantErr:   false,
		},
		{
			name: "transient_error_then_success",
			objects: []client.Object{
				&apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{Name: crdName},
				},
			},
			getErrs:   []error{apierrors.NewServiceUnavailable("unavailable")},
			wantFound: true,
			wantErr:   false,
		},
		{
			name: "permanent_error",
			getErrs: []error{
				apierrors.NewForbidden(
					schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
					crdName,
					errors.New("forbidden"),
				),
			},
			wantFound: false,
			wantErr:   true,
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
				Client:  fakeClient,
				getErrs: tt.getErrs,
			}

			m := &Migrator{
				client:       wrapped,
				log:          slog.Default(),
				retryBackoff: wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Jitter: 0, Cap: time.Millisecond},
			}

			found, err := m.crdExistsWithRetry(context.Background(), crdName, "test")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if found != tt.wantFound {
					t.Fatalf("found = %v, want %v", found, tt.wantFound)
				}
			}
		})
	}
}
