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
	"time"

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
			true,
			rvs.Status.SourceReplicaSnapshotName)
	}

	l := log.FromContext(rf.Ctx())
	l.Info("sync-mesh: enter",
		"rvs", rvs.Name,
		"childRVRSs", len(childRVRSs),
		"syncRevision", rvs.Status.SyncRevision,
		"syncTransitions", len(rvs.Status.SyncTransitions))

	allDRBDRs, err := r.listDRBDResources(rf.Ctx())
	if err != nil {
		return rf.Fail(err)
	}

	syncDRBDRNames := syncDRBDResourceNames(childRVRSs)
	syncDRBDRs := filterDRBDResourcesByName(allDRBDRs, syncDRBDRNames)
	originalNodeIDs := extractOriginalNodeIDs(allDRBDRs, childRVRSs)

	l.Info("sync-mesh: resources",
		"syncDRBDRNames", len(syncDRBDRNames),
		"syncDRBDRs", len(syncDRBDRs))

	base := rvs.DeepCopy()

	if allSyncTransitionsCompleted(rvs) {
		completeSyncStatus(rvs)
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
		return rf.Done()
	}

	if syncNeedsReset(rvs, syncDRBDRs, childRVRSs) {
		l.Info("sync-mesh: resetting stuck transitions (DRBDResources missing for >2m)")
		rvs.Status.SyncTransitions = nil
		rvs.Status.SyncRevision = 0
		rvs.Status.SyncCompletedReplicas = nil
		if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
		return rf.DoneAndRequeue()
	}

	if len(childRVRSs) < rvs.Status.Datamesh.TotalCount {
		return rf.DoneAndRequeue()
	}

	changed := snapmesh.ProcessSync(rf.Ctx(), rvs, rv, childRVRSs, syncDRBDRs, originalNodeIDs, r.cl, r.scheme)

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

func completeSyncStatus(rvs *v1alpha1.ReplicatedVolumeSnapshot) {
	rvs.Status.SyncDRBDResources = nil
	rvs.Status.SyncTransitions = nil
	rvs.Status.SyncRevision = 0
	rvs.Status.SyncCompletedReplicas = nil
	rvs.Status.Phase = v1alpha1.ReplicatedVolumeSnapshotPhaseReady
	rvs.Status.Message = "All replica snapshots are ready"
	rvs.Status.ReadyToUse = true
	if rvs.Status.CreationTime == nil {
		now := metav1.Now()
		rvs.Status.CreationTime = &now
	}
}

const syncStuckTimeout = 2 * time.Minute

func syncNeedsReset(
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	syncDRBDRs []*v1alpha1.DRBDResource,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) bool {
	if len(rvs.Status.SyncTransitions) == 0 {
		return false
	}

	rvrsNameByRVR := make(map[string]string, len(childRVRSs))
	childRVRNames := make(map[string]struct{}, len(childRVRSs))
	for _, rvrs := range childRVRSs {
		rvrsNameByRVR[rvrs.Spec.ReplicatedVolumeReplicaName] = rvrs.Name
		childRVRNames[rvrs.Spec.ReplicatedVolumeReplicaName] = struct{}{}
	}

	drbdrNames := make(map[string]struct{}, len(syncDRBDRs))
	for _, d := range syncDRBDRs {
		drbdrNames[d.Name] = struct{}{}
	}

	for i := range rvs.Status.SyncTransitions {
		t := &rvs.Status.SyncTransitions[i]

		pastStep0 := false
		var activeStart *metav1.Time
		for j := range t.Steps {
			s := &t.Steps[j]
			if s.Name == "Create resource" && s.Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
				pastStep0 = true
			}
			if s.Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive {
				activeStart = s.StartedAt
			}
		}

		if !pastStep0 || activeStart == nil {
			continue
		}

		if _, ok := childRVRNames[t.ReplicaName]; !ok {
			if time.Since(activeStart.Time) > syncStuckTimeout {
				return true
			}
			continue
		}

		rvrsName := rvrsNameByRVR[t.ReplicaName]
		if rvrsName == "" {
			continue
		}

		if _, ok := drbdrNames[rvrsName]; ok {
			continue
		}

		if time.Since(activeStart.Time) > syncStuckTimeout {
			return true
		}
	}

	return false
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

func (r *Reconciler) listDRBDResources(ctx context.Context) ([]v1alpha1.DRBDResource, error) {
	var list v1alpha1.DRBDResourceList
	if err := r.cl.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func filterDRBDResourcesByName(all []v1alpha1.DRBDResource, names []string) []*v1alpha1.DRBDResource {
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	result := make([]*v1alpha1.DRBDResource, 0, len(names))
	for i := range all {
		if _, ok := nameSet[all[i].Name]; ok {
			result = append(result, &all[i])
		}
	}
	return result
}

func extractOriginalNodeIDs(all []v1alpha1.DRBDResource, childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot) map[string]uint8 {
	rvrNames := make(map[string]struct{}, len(childRVRSs))
	for _, rvrs := range childRVRSs {
		rvrNames[rvrs.Spec.ReplicatedVolumeReplicaName] = struct{}{}
	}
	return extractOriginalNodeIDsForNames(all, rvrNames)
}

func extractOriginalNodeIDsForRVRs(all []v1alpha1.DRBDResource, rvrs []*v1alpha1.ReplicatedVolumeReplica) map[string]uint8 {
	rvrNames := make(map[string]struct{}, len(rvrs))
	for _, rvr := range rvrs {
		rvrNames[rvr.Name] = struct{}{}
	}
	return extractOriginalNodeIDsForNames(all, rvrNames)
}

func extractOriginalNodeIDsForNames(all []v1alpha1.DRBDResource, rvrNames map[string]struct{}) map[string]uint8 {
	result := make(map[string]uint8, len(rvrNames))
	for i := range all {
		if _, ok := rvrNames[all[i].Name]; ok {
			result[all[i].Name] = all[i].Spec.NodeID
		}
	}
	return result
}
