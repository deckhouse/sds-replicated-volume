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

package rvmetadata_test

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvmetadata "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_metadata"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

func withRVRIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolumeReplica{}, indexes.IndexFieldRVRByReplicatedVolumeName, func(obj client.Object) []string {
		rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
		if !ok {
			return nil
		}
		if rvr.Spec.ReplicatedVolumeName == "" {
			return nil
		}
		return []string{rvr.Spec.ReplicatedVolumeName}
	})
}

func TestReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	tests := []struct {
		name       string // description of this test case
		objects    []client.Object
		req        reconcile.Request
		want       reconcile.Result
		wantErr    bool
		wantFin    []string
		wantLabels map[string]string
	}{
		{
			name: "adds finalizer to new rv without rvrs",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-new",
						ResourceVersion: "1",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-new"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
		},
		{
			name: "adds finalizer and label when rsc specified",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-with-rsc",
						ResourceVersion: "1",
					},
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "my-storage-class",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-with-rsc"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
			wantLabels: map[string]string{
				v1alpha1.LabelReplicatedStorageClass: "my-storage-class",
			},
		},
		{
			name: "adds finalizer when rvr exists",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-with-rvr",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-linked",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-with-rvr",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-with-rvr"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
		},
		{
			name: "keeps finalizer when rv not deleting",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-with-finalizer",
						Finalizers:      []string{v1alpha1.ControllerAppFinalizer},
						ResourceVersion: "1",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-with-finalizer"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
		},
		{
			name: "removes finalizer when deleting and no rvrs",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rv-cleanup",
						Finalizers: []string{"other-finalizer", v1alpha1.ControllerAppFinalizer},
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						ResourceVersion: "1",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-cleanup"}},
			wantFin: []string{"other-finalizer"},
		},
		{
			name: "keeps finalizer while deleting",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rv-deleting",
						Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-for-deleting",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-deleting",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-deleting"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
		},
		{
			name: "does not add finalizer while deleting without rvrs",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
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
			wantFin: []string{"keep-me"},
		},
		{
			name: "does not change label if already set correctly",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-with-label",
						Finalizers:      []string{v1alpha1.ControllerAppFinalizer},
						ResourceVersion: "1",
						Labels: map[string]string{
							v1alpha1.LabelReplicatedStorageClass: "existing-class",
						},
					},
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "existing-class",
					},
				},
			},
			req:     reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-with-label"}},
			wantFin: []string{v1alpha1.ControllerAppFinalizer},
			wantLabels: map[string]string{
				v1alpha1.LabelReplicatedStorageClass: "existing-class",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := withRVRIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...)).
				Build()
			r := rvmetadata.NewReconciler(cl, slog.Default())
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

			rv := &v1alpha1.ReplicatedVolume{}
			if err := cl.Get(t.Context(), tt.req.NamespacedName, rv); err != nil {
				t.Fatalf("fetching rv: %v", err)
			}
			if !slices.Equal(rv.Finalizers, tt.wantFin) {
				t.Fatalf("finalizers mismatch: got %v, want %v", rv.Finalizers, tt.wantFin)
			}

			// Check labels if expected
			for key, wantValue := range tt.wantLabels {
				if gotValue := rv.Labels[key]; gotValue != wantValue {
					t.Errorf("label %s mismatch: got %q, want %q", key, gotValue, wantValue)
				}
			}
		})
	}
}
