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
	"sigs.k8s.io/controller-runtime/pkg/log"

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

	l := log.FromContext(rf.Ctx())
	l.Info("sync-mesh: enter",
		"rvs", rvs.Name,
		"childRVRSs", len(childRVRSs),
		"syncRevision", rvs.Status.SyncRevision,
		"syncTransitions", len(rvs.Status.SyncTransitions))

	syncDRBDRNames := syncDRBDResourceNames(childRVRSs)
	syncDRBDRs, err := r.getSyncDRBDResources(rf.Ctx(), syncDRBDRNames)
	if err != nil {
		return rf.Fail(err)
	}

	l.Info("sync-mesh: resources",
		"syncDRBDRNames", len(syncDRBDRNames),
		"syncDRBDRs", len(syncDRBDRs))

	base := rvs.DeepCopy()

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
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
		return rf.Done()
	}

	if syncResourcesMissing(rvs, syncDRBDRNames, syncDRBDRs) {
		rvs.Status.SyncTransitions = nil
		rvs.Status.SyncRevision = 0
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
		return rf.DoneAndRequeue()
	}

	changed := snapmesh.ProcessSync(rf.Ctx(), rvs, rv, childRVRSs, syncDRBDRs, r.cl, r.scheme)

	l.Info("sync-mesh: after ProcessSync",
		"changed", changed,
		"syncRevision", rvs.Status.SyncRevision,
		"syncTransitions", len(rvs.Status.SyncTransitions))

	if changed {
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			l.Error(err, "sync-mesh: patchRVSStatus failed")
			return rf.Fail(err)
		}
		l.Info("sync-mesh: status patched")
	}

	return rf.DoneAndRequeue()
}

func allSyncTransitionsCompleted(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return rvs.Status.SyncRevision > 0 && len(rvs.Status.SyncTransitions) == 0
}

func syncResourcesMissing(
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	expectedNames []string,
	actual []*v1alpha1.DRBDResource,
) bool {
	if len(rvs.Status.SyncTransitions) == 0 {
		return false
	}
	return len(actual) < len(expectedNames)
}

func syncDRBDResourceNames(childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot) []string {
	var names []string
	for _, rvrs := range childRVRSs {
		if rvrs.Status.Phase == v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady &&
			rvrs.Status.SnapshotHandle != "" {
			names = append(names, rvrs.Name)
		}
	}
	return names
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
