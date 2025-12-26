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

package rvstatusreplicas_test

import (
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvstatusreplicas "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_replicas"
)

func TestReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding scheme: %v", err)
	}

	tests := []struct {
		name         string // description of this test case
		objects      []client.Object
		req          reconcile.Request
		want         reconcile.Result
		wantErr      bool
		wantReplicas []v1alpha1.RVReplicaInfo
	}{
		{
			name: "rv not found returns no error",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "other-rv",
					},
				},
			},
			req:          reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-not-found"}},
			wantErr:      false,
			wantReplicas: nil,
		},
		{
			name: "rv with no rvrs sets empty replicas",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-no-rvrs",
						ResourceVersion: "1",
					},
				},
			},
			req:          reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-no-rvrs"}},
			wantReplicas: nil,
		},
		{
			name: "rv with single diskful rvr",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-single",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-diskful-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-single",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-single"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-diskful-1",
					NodeName:          "node-1",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rv with multiple rvrs of different types",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-multi",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-diskful",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-multi",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-access",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-multi",
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeAccess,
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-tiebreaker",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-multi",
						NodeName:             "node-3",
						Type:                 v1alpha1.ReplicaTypeTieBreaker,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-multi"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-diskful",
					NodeName:          "node-1",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
				{
					Name:              "rvr-access",
					NodeName:          "node-2",
					Type:              v1alpha1.ReplicaTypeAccess,
					DeletionTimestamp: nil,
				},
				{
					Name:              "rvr-tiebreaker",
					NodeName:          "node-3",
					Type:              v1alpha1.ReplicaTypeTieBreaker,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rv with rvr being deleted",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-deleting-rvr",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rvr-deleting",
						Finalizers: []string{"test-finalizer"},
						DeletionTimestamp: func() *metav1.Time {
							ts := metav1.NewTime(time.Now())
							return &ts
						}(),
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-deleting-rvr",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-active",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-deleting-rvr",
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-deleting-rvr"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:     "rvr-deleting",
					NodeName: "node-1",
					Type:     v1alpha1.ReplicaTypeDiskful,
					// DeletionTimestamp will be set (tested separately)
				},
				{
					Name:              "rvr-active",
					NodeName:          "node-2",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rv with existing status replicas gets updated",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-update",
						ResourceVersion: "1",
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						Replicas: []v1alpha1.RVReplicaInfo{
							{
								Name:     "rvr-old",
								NodeName: "node-old",
								Type:     v1alpha1.ReplicaTypeDiskful,
							},
						},
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-new",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-update",
						NodeName:             "node-new",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-update"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-new",
					NodeName:          "node-new",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "only includes rvrs matching rv name",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-target",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-for-target",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-target",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-for-other",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-other",
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-target"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-for-target",
					NodeName:          "node-1",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rv with nil status gets initialized",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-nil-status",
						ResourceVersion: "1",
					},
					Status: nil,
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-nil-status",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-nil-status"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-1",
					NodeName:          "node-1",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rvr with empty replicatedVolumeName is ignored",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-ignore",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-valid",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-ignore",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-empty-name",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "",
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-ignore"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-valid",
					NodeName:          "node-1",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rvr with empty nodeName is included",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-no-node",
						ResourceVersion: "1",
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-unscheduled",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-no-node",
						NodeName:             "",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-no-node"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-unscheduled",
					NodeName:          "",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "rv with other status fields preserves them",
			objects: []client.Object{
				&v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rv-preserve",
						ResourceVersion: "1",
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						Phase:       "Ready",
						PublishedOn: []string{"node-1"},
						Replicas: []v1alpha1.RVReplicaInfo{
							{Name: "old-rvr"},
						},
					},
				},
				&v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-new",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "rv-preserve",
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				},
			},
			req: reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-preserve"}},
			wantReplicas: []v1alpha1.RVReplicaInfo{
				{
					Name:              "rvr-new",
					NodeName:          "node-2",
					Type:              v1alpha1.ReplicaTypeDiskful,
					DeletionTimestamp: nil,
				},
			},
		},
		{
			name: "large number of rvrs",
			objects: func() []client.Object {
				objs := []client.Object{
					&v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "rv-many",
							ResourceVersion: "1",
						},
					},
				}
				for i := 0; i < 100; i++ {
					objs = append(objs, &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rvr-%d", i),
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "rv-many",
							NodeName:             fmt.Sprintf("node-%d", i%10),
							Type:                 v1alpha1.ReplicaTypeDiskful,
						},
					})
				}
				return objs
			}(),
			req:          reconcile.Request{NamespacedName: types.NamespacedName{Name: "rv-many"}},
			wantReplicas: nil, // We'll check count separately
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
				WithIndex(
					&v1alpha1.ReplicatedVolumeReplica{},
					rvstatusreplicas.IndexRVRByReplicatedVolumeName,
					rvstatusreplicas.IndexRVRByReplicatedVolumeNameFunc,
				).
				Build()
			r := rvstatusreplicas.NewReconciler(cl, slog.Default())
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

			// Skip validation if wantReplicas is nil and RV not found
			if tt.wantReplicas == nil && tt.name == "rv not found returns no error" {
				return
			}

			rv := &v1alpha1.ReplicatedVolume{}
			if err := cl.Get(t.Context(), tt.req.NamespacedName, rv); err != nil {
				t.Fatalf("fetching rv: %v", err)
			}

			// Special handling for "large number of rvrs" test
			if tt.name == "large number of rvrs" {
				if rv.Status == nil || len(rv.Status.Replicas) != 100 {
					t.Fatalf("expected exactly 100 replicas, got %d", len(rv.Status.Replicas))
				}
				return
			}

			// Special handling for "rv with other status fields preserves them" test
			if tt.name == "rv with other status fields preserves them" {
				if rv.Status.Phase != "Ready" {
					t.Errorf("Phase was overwritten: got %s, want Ready", rv.Status.Phase)
				}
				if len(rv.Status.PublishedOn) != 1 || rv.Status.PublishedOn[0] != "node-1" {
					t.Errorf("PublishedOn was overwritten: got %v, want [node-1]", rv.Status.PublishedOn)
				}
			}

			if rv.Status == nil {
				if tt.wantReplicas != nil {
					t.Fatalf("status is nil, but expected replicas: %v", tt.wantReplicas)
				}
				return
			}

			// Compare replicas, ignoring DeletionTimestamp for simplicity in most cases
			// (we test it separately with the "rv with rvr being deleted" case)
			if len(rv.Status.Replicas) != len(tt.wantReplicas) {
				t.Fatalf("replicas count mismatch: got %d, want %d\nGot: %+v\nWant: %+v",
					len(rv.Status.Replicas), len(tt.wantReplicas), rv.Status.Replicas, tt.wantReplicas)
			}

			// For the deleting rvr test, check DeletionTimestamp separately
			if tt.name == "rv with rvr being deleted" {
				found := false
				for _, r := range rv.Status.Replicas {
					if r.Name == "rvr-deleting" {
						found = true
						if r.DeletionTimestamp == nil {
							t.Errorf("rvr-deleting should have DeletionTimestamp set")
						}
					}
				}
				if !found {
					t.Errorf("rvr-deleting not found in status.replicas")
				}
			}

			// Compare without considering order
			for i, want := range tt.wantReplicas {
				found := false
				for _, got := range rv.Status.Replicas {
					gotCopy := got
					wantCopy := want
					// For non-deleting test cases, ignore DeletionTimestamp in comparison
					if tt.name != "rv with rvr being deleted" {
						gotCopy.DeletionTimestamp = nil
						wantCopy.DeletionTimestamp = nil
					} else {
						// For the deleting case, only check if DeletionTimestamp is set/not set
						// Don't compare the exact timestamp value
						if want.Name == "rvr-deleting" && got.Name == "rvr-deleting" {
							if got.DeletionTimestamp != nil {
								gotCopy.DeletionTimestamp = nil
								wantCopy.DeletionTimestamp = nil
							}
						} else {
							gotCopy.DeletionTimestamp = nil
							wantCopy.DeletionTimestamp = nil
						}
					}
					if reflect.DeepEqual(gotCopy, wantCopy) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("replica %d not found: want %+v\nGot replicas: %+v", i, want, rv.Status.Replicas)
				}
			}
		})
	}
}
