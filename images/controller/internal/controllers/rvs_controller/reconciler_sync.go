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
	"crypto/rand"
	"encoding/base64"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

	syncNames := rvs.Status.SyncDRBDResources
	if len(syncNames) == 0 {
		return r.createSyncDRBDResources(rf.Ctx(), rvs, rv, childRVRSs)
	}

	syncDRBDRs, allExist, err := r.getSyncDRBDResources(rf.Ctx(), syncNames)
	if err != nil {
		return rf.Fail(err)
	}

	if !allExist && allGone(syncDRBDRs) {
		return r.finalizeSyncComplete(rf.Ctx(), rvs)
	}

	if !allExist {
		return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
			"Waiting for sync resources cleanup",
			false)
	}

	if !allHaveAddresses(syncDRBDRs) {
		return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
			"Waiting for sync resources to allocate addresses",
			false)
	}

	if !allPeersWired(syncDRBDRs) {
		return r.wireSyncPeers(rf.Ctx(), syncDRBDRs)
	}

	if isSyncComplete(syncDRBDRs) {
		return r.cleanupSyncResources(rf.Ctx(), rvs, syncDRBDRs)
	}

	return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
		v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
		"Synchronizing snapshot data across replicas",
		false)
}

func (r *Reconciler) createSyncDRBDResources(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "sync-create")
	defer rf.OnEnd(&outcome)

	size := rv.Status.Datamesh.Size.DeepCopy()
	systemNetworks := slices.Clone(rv.Status.Datamesh.SystemNetworkNames)

	var syncNames []string
	for _, rvrs := range childRVRSs {
		if rvrs.Status.SnapshotHandle == "" {
			continue
		}

		nodeID := v1alpha1.IDFromName(rvrs.Spec.ReplicatedVolumeReplicaName)

		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{
				Name: rvrs.Name,
			},
			Spec: v1alpha1.DRBDResourceSpec{
				State:                        v1alpha1.DRBDResourceStateUp,
				NodeName:                     rvrs.Spec.NodeName,
				SystemNetworks:               systemNetworks,
				Type:                         v1alpha1.DRBDResourceTypeDiskful,
				LVMLogicalVolumeSnapshotName: rvrs.Status.SnapshotHandle,
				PreserveExistingMetadata:     true,
				NodeID:                       nodeID,
				Size:                         &size,
				Role:                         v1alpha1.DRBDRoleSecondary,
			},
		}

		if _, err := obju.SetControllerRef(drbdr, rvs, r.scheme); err != nil {
			return rf.Fail(err)
		}

		if err := r.cl.Create(rf.Ctx(), drbdr); err != nil {
			if apierrors.IsAlreadyExists(err) {
				syncNames = append(syncNames, rvrs.Name)
				continue
			}
			return rf.Fail(err)
		}
		syncNames = append(syncNames, rvrs.Name)
	}

	base := rvs.DeepCopy()
	rvs.Status.SyncDRBDResources = syncNames
	rvs.Status.Phase = v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing
	rvs.Status.Message = "Sync resources created, waiting for address allocation"
	if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
		return rf.Fail(err)
	}

	return rf.DoneAndRequeue()
}

func (r *Reconciler) wireSyncPeers(
	ctx context.Context,
	syncDRBDRs []*v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "sync-wire-peers")
	defer rf.OnEnd(&outcome)

	secret := extractSharedSecret(syncDRBDRs)
	if secret == "" {
		var err error
		secret, err = generateSyncSharedSecret()
		if err != nil {
			return rf.Fail(err)
		}
	}

	for _, drbdr := range syncDRBDRs {
		peers := buildSyncPeers(drbdr, syncDRBDRs, secret)
		if peersEqual(drbdr.Spec.Peers, peers) {
			continue
		}

		base := drbdr.DeepCopy()
		drbdr.Spec.Peers = peers
		patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
		if err := r.cl.Patch(rf.Ctx(), drbdr, patch); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.DoneAndRequeue()
}

func (r *Reconciler) cleanupSyncResources(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	syncDRBDRs []*v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "sync-cleanup")
	defer rf.OnEnd(&outcome)

	for _, drbdr := range syncDRBDRs {
		if drbdr.DeletionTimestamp != nil {
			continue
		}
		if err := r.cl.Delete(rf.Ctx(), drbdr); err != nil && !apierrors.IsNotFound(err) {
			return rf.Fail(err)
		}
	}

	return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
		v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
		"Waiting for sync resources cleanup",
		false)
}

func (r *Reconciler) finalizeSyncComplete(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "sync-finalize")
	defer rf.OnEnd(&outcome)

	base := rvs.DeepCopy()
	rvs.Status.SyncDRBDResources = nil
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

// ──────────────────────────────────────────────────────────────────────────────
// I/O helpers
//

func (r *Reconciler) getSyncDRBDResources(ctx context.Context, names []string) (result []*v1alpha1.DRBDResource, allExist bool, err error) {
	drbdrList := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, drbdrList); err != nil {
		return nil, false, err
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[name] = struct{}{}
	}

	result = make([]*v1alpha1.DRBDResource, 0, len(names))
	for i := range drbdrList.Items {
		if _, ok := nameSet[drbdrList.Items[i].Name]; ok {
			result = append(result, &drbdrList.Items[i])
		}
	}

	return result, len(result) == len(names), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers
//

func generateSyncSharedSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(buf), nil
}

func extractSharedSecret(syncDRBDRs []*v1alpha1.DRBDResource) string {
	for _, drbdr := range syncDRBDRs {
		if len(drbdr.Spec.Peers) > 0 {
			return drbdr.Spec.Peers[0].SharedSecret
		}
	}
	return ""
}

func allGone(syncDRBDRs []*v1alpha1.DRBDResource) bool {
	return len(syncDRBDRs) == 0
}

func allHaveAddresses(syncDRBDRs []*v1alpha1.DRBDResource) bool {
	for _, drbdr := range syncDRBDRs {
		if len(drbdr.Status.Addresses) == 0 {
			return false
		}
	}
	return true
}

func allPeersWired(syncDRBDRs []*v1alpha1.DRBDResource) bool {
	for _, drbdr := range syncDRBDRs {
		if len(drbdr.Spec.Peers) == 0 {
			return false
		}
	}
	return true
}

func isSyncComplete(syncDRBDRs []*v1alpha1.DRBDResource) bool {
	for _, drbdr := range syncDRBDRs {
		if drbdr.Status.DiskState != v1alpha1.DiskStateUpToDate {
			return false
		}
		for _, peer := range drbdr.Status.Peers {
			if peer.DiskState != v1alpha1.DiskStateUpToDate {
				return false
			}
		}
	}
	return true
}

func buildSyncPeers(drbdr *v1alpha1.DRBDResource, syncDRBDRs []*v1alpha1.DRBDResource, sharedSecret string) []v1alpha1.DRBDResourcePeer {
	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(syncDRBDRs)-1)
	for _, peer := range syncDRBDRs {
		if peer.Name == drbdr.Name {
			continue
		}
		paths := make([]v1alpha1.DRBDResourcePath, len(peer.Status.Addresses))
		for i, addr := range peer.Status.Addresses {
			paths[i] = v1alpha1.DRBDResourcePath(addr)
		}
		peers = append(peers, v1alpha1.DRBDResourcePeer{
			Name:            peer.Name,
			Type:            v1alpha1.DRBDResourceTypeDiskful,
			AllowRemoteRead: true,
			NodeID:          peer.Spec.NodeID,
			Protocol:        v1alpha1.DRBDProtocolC,
			SharedSecret:    sharedSecret,
			SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
			Paths:           paths,
		})
	}
	return peers
}

func peersEqual(a, b []v1alpha1.DRBDResourcePeer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if a[i].NodeID != b[i].NodeID {
			return false
		}
		if a[i].SharedSecret != b[i].SharedSecret {
			return false
		}
		if len(a[i].Paths) != len(b[i].Paths) {
			return false
		}
		for j := range a[i].Paths {
			if a[i].Paths[j].SystemNetworkName != b[i].Paths[j].SystemNetworkName {
				return false
			}
			if a[i].Paths[j].Address.IPv4 != b[i].Paths[j].Address.IPv4 {
				return false
			}
			if a[i].Paths[j].Address.Port != b[i].Paths[j].Address.Port {
				return false
			}
		}
	}
	return true
}
