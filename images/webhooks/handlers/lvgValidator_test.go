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
	"encoding/json"
	"reflect"
	"testing"

	"github.com/slok/kubewebhook/v2/pkg/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func lvgWithPools(pools ...string) *snc.LVMVolumeGroup {
	lvg := &snc.LVMVolumeGroup{ObjectMeta: metav1.ObjectMeta{Name: "vg-a"}}
	for _, p := range pools {
		lvg.Spec.ThinPools = append(lvg.Spec.ThinPools, snc.LVMVolumeGroupThinPoolSpec{Name: p, Size: "1Gi"})
	}
	return lvg
}

func TestRemovedThinPools(t *testing.T) {
	got := removedThinPools(lvgWithPools("kept", "gone"), lvgWithPools("kept", "added"))
	if want := map[string]struct{}{"gone": {}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removedThinPools() = %v, want %v", got, want)
	}
}

// A removed pool counts as used when a pool or a class takes it from this
// LVMVolumeGroup; the same pool name on another group, a pool that stays, and a
// resource being deleted do not count.
func TestThinPoolUses(t *testing.T) {
	removed := map[string]struct{}{"gone": {}}
	now := metav1.Now()

	deletingRSP := srv.ReplicatedStoragePool{ObjectMeta: metav1.ObjectMeta{Name: "deleting", DeletionTimestamp: &now}}
	deletingRSP.Spec.LVMVolumeGroups = []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a", ThinPoolName: "gone"}}

	rsps := []srv.ReplicatedStoragePool{
		{ObjectMeta: metav1.ObjectMeta{Name: "uses"}, Spec: srv.ReplicatedStoragePoolSpec{
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a", ThinPoolName: "gone"}},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other-group"}, Spec: srv.ReplicatedStoragePoolSpec{
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-b", ThinPoolName: "gone"}},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other-pool"}, Spec: srv.ReplicatedStoragePoolSpec{
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a", ThinPoolName: "kept"}},
		}},
		deletingRSP,
	}
	rscs := []srv.ReplicatedStorageClass{
		{ObjectMeta: metav1.ObjectMeta{Name: "embedded"}, Spec: srv.ReplicatedStorageClassSpec{
			Storage: &srv.ReplicatedStorageClassStorage{
				Type:            "LVMThin",
				LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a", ThinPoolName: "gone"}},
			},
		}},
		{ObjectMeta: metav1.ObjectMeta{Name: "no-storage"}},
	}

	got := thinPoolUses("vg-a", removed, rsps, rscs)
	want := []string{
		"thin pool gone by the ReplicatedStorageClass embedded",
		"thin pool gone by the ReplicatedStoragePool uses",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("thinPoolUses() = %v, want %v", got, want)
	}
}

// An update that removes no thin pool, a create, and a group being deleted are
// let through without looking at the pools and classes.
func TestLVGValidate_PassesWhatRemovesNoPool(t *testing.T) {
	oldRaw, err := json.Marshal(lvgWithPools("kept"))
	if err != nil {
		t.Fatal(err)
	}

	now := metav1.Now()
	deleting := lvgWithPools()
	deleting.DeletionTimestamp = &now

	for name, tc := range map[string]struct {
		ar  *model.AdmissionReview
		obj *snc.LVMVolumeGroup
	}{
		"nothing removed": {&model.AdmissionReview{Operation: model.OperationUpdate, OldObjectRaw: oldRaw}, lvgWithPools("kept", "added")},
		"create":          {&model.AdmissionReview{Operation: model.OperationCreate}, lvgWithPools()},
		"being deleted":   {&model.AdmissionReview{Operation: model.OperationUpdate, OldObjectRaw: oldRaw}, deleting},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := LVGValidate(context.Background(), tc.ar, tc.obj)
			if err != nil {
				t.Fatalf("LVGValidate() error = %v", err)
			}
			if !res.Valid {
				t.Fatalf("LVGValidate() rejected: %s", res.Message)
			}
		})
	}
}
