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

package rvscontroller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvs_controller/snapmesh"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

func (r *Reconciler) reconcileSyncMesh(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "sync-mesh")
	defer rf.OnEnd(&outcome)

	if len(childRVRSs) <= 1 {
		return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseReady,
			"All replica snapshots are ready",
			true)
	}

	syncDRBDRs, err := r.getSyncDRBDResources(rf.Ctx(), rvs.Status.SyncDRBDResources)
	if err != nil {
		return rf.Fail(err)
	}

	base := rvs.DeepCopy()
	changed := snapmesh.ProcessSync(rf.Ctx(), rvs, rv, childRVRSs, syncDRBDRs, r.cl, r.scheme)

	if allSyncTransitionsCompleted(rvs) {
		rvs.Status.SyncDRBDResources = nil
		rvs.Status.SyncTransitions = nil
		rvs.Status.SyncRevision = 0
		rvs.Status.Phase = v1alpha1.ReplicatedVolumeSnapshotPhaseReady
		rvs.Status.Message = "All replica snapshots are ready"
		rvs.Status.ReadyToUse = true
		if rvs.Status.CreationTime == nil {
			now := metav1.Now()
			rvs.Status.CreationTime = &now
		}
		changed = true
	}

	if changed {
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.DoneAndRequeue()
}

func allSyncTransitionsCompleted(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return rvs.Status.SyncRevision > 0 && len(rvs.Status.SyncTransitions) == 0
}

func (r *Reconciler) getSyncDRBDResources(ctx context.Context, names []string) ([]*v1alpha1.DRBDResource, error) {
	if len(names) == 0 {
		return nil, nil
	}

	drbdrList := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, drbdrList); err != nil {
		return nil, err
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[name] = struct{}{}
	}

	result := make([]*v1alpha1.DRBDResource, 0, len(names))
	for i := range drbdrList.Items {
		if _, ok := nameSet[drbdrList.Items[i].Name]; ok {
			result = append(result, &drbdrList.Items[i])
		}
	}

	return result, nil
}
