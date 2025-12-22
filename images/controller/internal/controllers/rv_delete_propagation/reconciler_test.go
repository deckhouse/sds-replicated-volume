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

package rvdeletepropagation_test

import (
	"log/slog"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvdeletepropagation "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_delete_propagation"
)

func TestReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	tests := []struct {
		name            string // description of this test case
		objects         []client.Object
		req             reconcile.Request
		want            reconcile.Result
		wantErr         bool
		expectDeleted   []types.NamespacedName
		expectRemaining []types.NamespacedName
	}{
		{
			name: "deletes linked rvrs for active rv",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-active",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-linked",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-active",
						Type:                 "Diskful",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-other",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-other",
						Type:                 "Diskful",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-already-deleting",
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						Finalizers: []string{"keep-me"},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-active",
						Type:                 "Diskful",
					},
				},
			},
			req:           reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-active"}},
			expectDeleted: []types.NamespacedName{{Name: "rvr-linked"}},
			expectRemaining: []types.NamespacedName{
				{Name: "rvr-other"},
				{Name: "rvr-already-deleting"},
			},
		},
		{
			name: "skips deletion when rv is being removed",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rv-deleting",
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						Finalizers:      []string{"keep-me"},
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-linked",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-deleting",
						Type:                 "Diskful",
					},
				},
			},
			req:             reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-deleting"}},
			expectRemaining: []types.NamespacedName{{Name: "rvr-linked"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			r := rvdeletepropagation.NewReconciler(cl, slog.Default())
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

			for _, nn := range tt.expectDeleted {
				rvr := &v1alpha1.ReplicatedVolumeReplica{}
				err := cl.Get(t.Context(), nn, rvr)
				if err == nil {
					t.Fatalf("expected rvr %s to be deleted, but it still exists", nn.Name)
				}
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expected not found for rvr %s, got %v", nn.Name, err)
				}
			}

			for _, nn := range tt.expectRemaining {
				rvr := &v1alpha1.ReplicatedVolumeReplica{}
				if err := cl.Get(t.Context(), nn, rvr); err != nil {
					t.Fatalf("expected rvr %s to remain, get err: %v", nn.Name, err)
				}
			}
		})
	}
}
