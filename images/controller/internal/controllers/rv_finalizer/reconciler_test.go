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

package rvfinalizer_test

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvfinalizer "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_finalizer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha3.AddToScheme(scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	tests := []struct {
		name    string // description of this test case
		objects []client.Object
		req     reconcile.Request
		want    reconcile.Result
		wantErr bool
		wantFin []string
	}{
		{
			name: "adds finalizer when rvr exists",
			objects: []client.Object{
				&v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-with-rvr",
						ResourceVersion: "1",
					},
				},
				&v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-linked",
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-with-rvr",
						Type:                 "Diskful",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-with-rvr"}},
			wantFin: []string{v1alpha3.ControllerAppFinalizer},
		},
		{
			name: "removes finalizer when no rvrs",
			objects: []client.Object{
				&v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-cleanup",
						Finalizers:      []string{v1alpha3.ControllerAppFinalizer},
						ResourceVersion: "1",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-cleanup"}},
			wantFin: nil,
		},
		{
			name: "keeps finalizer while deleting",
			objects: []client.Object{
				&v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rv-deleting",
						Finalizers: []string{v1alpha3.ControllerAppFinalizer},
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						ResourceVersion: "1",
					},
				},
				&v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-for-deleting",
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-deleting",
						Type:                 "Diskful",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-deleting"}},
			wantFin: []string{v1alpha3.ControllerAppFinalizer},
		},
		{
			name: "adds finalizer while deleting without rvrs",
			objects: []client.Object{
				&v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-newly-deleting",
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						Finalizers:      []string{"keep-me"},
						ResourceVersion: "1",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-newly-deleting"}},
			wantFin: []string{"keep-me", v1alpha3.ControllerAppFinalizer},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()
			r := rvfinalizer.NewReconciler(cl, slog.Default())
			got, gotErr := r.Reconcile(t.Context(), tt.req)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Reconcile() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Reconcile() succeeded unexpectedly")
			}
			if got != tt.want {
				t.Errorf("Reconcile() = %v, want %v", got, tt.want)
			}

			rv := &v1alpha3.ReplicatedVolume{}
			if err := cl.Get(t.Context(), tt.req.NamespacedName, rv); err != nil {
				t.Fatalf("fetching rv: %v", err)
			}
			if !slices.Equal(rv.Finalizers, tt.wantFin) {
				t.Fatalf("finalizers mismatch: got %v, want %v", rv.Finalizers, tt.wantFin)
			}
		})
	}
}
