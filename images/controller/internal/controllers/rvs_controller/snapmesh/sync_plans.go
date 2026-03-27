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

package snapmesh

import (
	"crypto/rand"
	"encoding/base64"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

const (
	syncTransitionType = v1alpha1.ReplicatedVolumeDatameshTransitionTypeSyncSnapshot
	syncPlanID         = dmte.PlanID("sync/v1")
)

func registerSyncPlans(reg *dmte.Registry[*globalContext, *replicaContext]) {
	rt := reg.ReplicaTransition(syncTransitionType, syncSlot)

	rt.Plan(syncPlanID).
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync).
		DisplayName("Synchronizing snapshot").
		Steps(
			dmte.ReplicaStep("Create resource", applyNoop, confirmCreate),
			dmte.ReplicaStep("Wait addresses", applyNoop, confirmWaitAddresses),
			dmte.ReplicaStep("Wire peers", applyNoop, confirmWirePeers),
			dmte.ReplicaStep("Wait sync", applyNoop, confirmWaitSync),
			dmte.ReplicaStep("Cleanup", applyNoop, confirmCleanup),
		).
		Build()
}

func applyNoop(_ *globalContext, _ *replicaContext) bool { return false }

// ──────────────────────────────────────────────────────────────────────────────
// Step 1: Create resource
//
// Creates the temporary sync DRBDResource for this replica's snapshot.
// I/O is in confirm (not apply) so it retries on every reconcile if creation
// fails. Returns confirmed once the DRBDResource exists.
//

func confirmCreate(gctx *globalContext, rctx *replicaContext, _ int64) dmte.ConfirmResult {
	must := idset.Of(rctx.id)
	if rctx.drbdr != nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}

	size := gctx.rv.Status.Datamesh.Size.DeepCopy()
	systemNetworks := slices.Clone(gctx.rv.Status.Datamesh.SystemNetworkNames)

	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: rctx.rvrsName,
		},
		Spec: v1alpha1.DRBDResourceSpec{
			State:                        v1alpha1.DRBDResourceStateUp,
			NodeName:                     rctx.nodeName,
			SystemNetworks:               systemNetworks,
			Type:                         v1alpha1.DRBDResourceTypeDiskful,
			LVMLogicalVolumeSnapshotName: rctx.rvrs.Status.SnapshotHandle,
			PreserveExistingMetadata:     true,
			NodeID:                       rctx.id,
			Size:                         &size,
			Role:                         v1alpha1.DRBDRoleSecondary,
		},
	}

	if _, err := obju.SetControllerRef(drbdr, gctx.rvs, gctx.scheme); err != nil {
		return dmte.ConfirmResult{MustConfirm: must}
	}

	if err := gctx.cl.Create(gctx.ctx, drbdr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return dmte.ConfirmResult{MustConfirm: must}
		}
		return dmte.ConfirmResult{MustConfirm: must}
	}

	rctx.drbdr = drbdr
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
}

// ──────────────────────────────────────────────────────────────────────────────
// Step 2: Wait addresses
//
// Blocks until ALL replicas have allocated DRBD addresses.
// MustConfirm = all replica IDs, so all transitions advance to step 3
// simultaneously, guaranteeing all addresses are available for peer wiring.
//

func confirmWaitAddresses(gctx *globalContext, _ *replicaContext, _ int64) dmte.ConfirmResult {
	var must, confirmed idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		must.Add(rc.id)
		if rc.drbdr != nil && len(rc.drbdr.Status.Addresses) > 0 {
			confirmed.Add(rc.id)
		}
	}
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: confirmed}
}

// ──────────────────────────────────────────────────────────────────────────────
// Step 3: Wire peers
//
// Patches this replica's DRBDResource with peer connections to all other
// replicas. I/O is in confirm so it retries if the patch fails.
// A shared secret is generated once and reused across all replicas.
//

func confirmWirePeers(gctx *globalContext, rctx *replicaContext, _ int64) dmte.ConfirmResult {
	must := idset.Of(rctx.id)

	if rctx.drbdr != nil && len(rctx.drbdr.Spec.Peers) > 0 {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}

	if rctx.drbdr == nil {
		return dmte.ConfirmResult{MustConfirm: must}
	}

	if gctx.sharedSecret == "" {
		gctx.sharedSecret = extractSharedSecret(gctx)
		if gctx.sharedSecret == "" {
			secret, err := generateSyncSharedSecret()
			if err != nil {
				return dmte.ConfirmResult{MustConfirm: must}
			}
			gctx.sharedSecret = secret
		}
	}

	peers := buildSyncPeers(rctx, gctx)
	base := rctx.drbdr.DeepCopy()
	rctx.drbdr.Spec.Peers = peers
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := gctx.cl.Patch(gctx.ctx, rctx.drbdr, patch); err != nil {
		rctx.drbdr.Spec.Peers = base.Spec.Peers
		return dmte.ConfirmResult{MustConfirm: must}
	}

	return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
}

// ──────────────────────────────────────────────────────────────────────────────
// Step 4: Wait sync
//
// Blocks until ALL replicas report UpToDate disk state and all peers are
// UpToDate. MustConfirm = all, so all transitions advance to cleanup together.
//

func confirmWaitSync(gctx *globalContext, _ *replicaContext, _ int64) dmte.ConfirmResult {
	var must, confirmed idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		must.Add(rc.id)
		if rc.drbdr != nil && isReplicaSynced(rc.drbdr) {
			confirmed.Add(rc.id)
		}
	}
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: confirmed}
}

// ──────────────────────────────────────────────────────────────────────────────
// Step 5: Cleanup
//
// Deletes the temporary sync DRBDResource. I/O is in confirm so it retries
// if deletion fails. Returns confirmed once the DRBDResource is gone.
//

func confirmCleanup(gctx *globalContext, rctx *replicaContext, _ int64) dmte.ConfirmResult {
	must := idset.Of(rctx.id)
	if rctx.drbdr == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}

	if rctx.drbdr.DeletionTimestamp == nil {
		if err := gctx.cl.Delete(gctx.ctx, rctx.drbdr); err != nil && !apierrors.IsNotFound(err) {
			return dmte.ConfirmResult{MustConfirm: must}
		}
	}

	return dmte.ConfirmResult{MustConfirm: must}
}

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers
//

func isReplicaSynced(drbdr *v1alpha1.DRBDResource) bool {
	if drbdr.Status.DiskState != v1alpha1.DiskStateUpToDate {
		return false
	}
	for _, peer := range drbdr.Status.Peers {
		if peer.DiskState != v1alpha1.DiskStateUpToDate {
			return false
		}
	}
	return true
}

func extractSharedSecret(gctx *globalContext) string {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.drbdr != nil && len(rc.drbdr.Spec.Peers) > 0 {
			return rc.drbdr.Spec.Peers[0].SharedSecret
		}
	}
	return ""
}

func generateSyncSharedSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(buf), nil
}

func buildSyncPeers(self *replicaContext, gctx *globalContext) []v1alpha1.DRBDResourcePeer {
	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(gctx.allReplicas)-1)
	for i := range gctx.allReplicas {
		peer := &gctx.allReplicas[i]
		if peer.id == self.id || peer.drbdr == nil || len(peer.drbdr.Status.Addresses) == 0 {
			continue
		}
		paths := make([]v1alpha1.DRBDResourcePath, len(peer.drbdr.Status.Addresses))
		for j, addr := range peer.drbdr.Status.Addresses {
			paths[j] = v1alpha1.DRBDResourcePath(addr)
		}
		peers = append(peers, v1alpha1.DRBDResourcePeer{
			Name:            peer.drbdr.Name,
			Type:            v1alpha1.DRBDResourceTypeDiskful,
			AllowRemoteRead: true,
			NodeID:          peer.drbdr.Spec.NodeID,
			Protocol:        v1alpha1.DRBDProtocolC,
			SharedSecret:    gctx.sharedSecret,
			SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
			Paths:           paths,
		})
	}
	return peers
}
