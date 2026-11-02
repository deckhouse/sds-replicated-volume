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
	"fmt"
	"sort"
	"strings"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// LVGValidate keeps a thin pool that a ReplicatedStoragePool or a
// ReplicatedStorageClass uses in spec.thinPools of its LVMVolumeGroup.
// sds-node-configurator removes a thin pool dropped from the spec as soon as it
// holds no volumes, and the pool would then place replicas in a thin pool that
// is gone.
func LVGValidate(ctx context.Context, ar *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	lvg, ok := obj.(*snc.LVMVolumeGroup)
	if !ok || ar.Operation != model.OperationUpdate || lvg.GetDeletionTimestamp() != nil {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	oldLVG := &snc.LVMVolumeGroup{}
	if err := json.Unmarshal(ar.OldObjectRaw, oldLVG); err != nil {
		return nil, fmt.Errorf("unable to decode the old LVMVolumeGroup: %w", err)
	}

	removed := removedThinPools(oldLVG, lvg)
	if len(removed) == 0 {
		return &kwhvalidating.ValidatorResult{Valid: true}, nil
	}

	cl, err := NewKubeClient("")
	if err != nil {
		return nil, fmt.Errorf("unable to build a kube client: %w", err)
	}

	rsps := &srv.ReplicatedStoragePoolList{}
	if err := cl.List(ctx, rsps); err != nil {
		return nil, fmt.Errorf("unable to list the ReplicatedStoragePools: %w", err)
	}
	rscs := &srv.ReplicatedStorageClassList{}
	if err := cl.List(ctx, rscs); err != nil {
		return nil, fmt.Errorf("unable to list the ReplicatedStorageClasses: %w", err)
	}

	if uses := thinPoolUses(lvg.Name, removed, rsps.Items, rscs.Items); len(uses) > 0 {
		return &kwhvalidating.ValidatorResult{
			Valid: false,
			Message: fmt.Sprintf("thin pools of the LVMVolumeGroup %s are in use: %s; remove them from those resources first",
				lvg.Name, strings.Join(uses, "; ")),
		}, nil
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

// removedThinPools returns the names of the thin pools in oldLVG's spec that
// newLVG's spec no longer has.
func removedThinPools(oldLVG, newLVG *snc.LVMVolumeGroup) map[string]struct{} {
	kept := make(map[string]struct{}, len(newLVG.Spec.ThinPools))
	for _, tp := range newLVG.Spec.ThinPools {
		kept[tp.Name] = struct{}{}
	}

	removed := make(map[string]struct{})
	for _, tp := range oldLVG.Spec.ThinPools {
		if _, ok := kept[tp.Name]; !ok {
			removed[tp.Name] = struct{}{}
		}
	}
	return removed
}

// thinPoolUses returns "pool used by resource" for every pool or class that is
// not being deleted and takes one of the removed pools of the LVMVolumeGroup.
func thinPoolUses(lvgName string, removed map[string]struct{}, rsps []srv.ReplicatedStoragePool, rscs []srv.ReplicatedStorageClass) []string {
	var uses []string
	match := func(kind, name string, groups []srv.ReplicatedStoragePoolLVMVolumeGroups) {
		for _, g := range groups {
			if g.Name != lvgName {
				continue
			}
			if _, ok := removed[g.ThinPoolName]; ok {
				uses = append(uses, fmt.Sprintf("thin pool %s by the %s %s", g.ThinPoolName, kind, name))
			}
		}
	}

	for _, rsp := range rsps {
		if rsp.GetDeletionTimestamp() == nil {
			match("ReplicatedStoragePool", rsp.Name, rsp.Spec.LVMVolumeGroups)
		}
	}
	for _, rsc := range rscs {
		if rsc.GetDeletionTimestamp() == nil && rsc.Spec.Storage != nil {
			match("ReplicatedStorageClass", rsc.Name, rsc.Spec.Storage.LVMVolumeGroups)
		}
	}

	sort.Strings(uses)
	return uses
}
