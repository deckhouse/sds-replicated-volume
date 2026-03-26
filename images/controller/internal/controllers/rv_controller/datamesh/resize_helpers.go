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

package datamesh

import (
	"fmt"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
)

// ──────────────────────────────────────────────────────────────────────────────
// Resize guards
//

// guardHasReadyDiskfulMember blocks if no diskful member has Ready=True.
// There is no point starting resize if no D replica is operational.
func guardHasReadyDiskfulMember(gctx *globalContext) dmte.GuardResult {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.HasBackingVolume() || rc.rvr == nil {
			continue
		}
		if obju.StatusCondition(rc.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).IsTrue().Eval() {
			return dmte.GuardResult{}
		}
	}
	return dmte.GuardResult{
		Blocked: true,
		Message: "no ready diskful member",
	}
}

// guardNoActiveResync blocks if any diskful member has a peer in a syncing
// replication state. Resize must not run concurrently with resync because
// DRBD resize interacts poorly with active synchronization.
func guardNoActiveResync(gctx *globalContext) dmte.GuardResult {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.HasBackingVolume() || rc.rvr == nil {
			continue
		}
		for _, peer := range rc.rvr.Status.Peers {
			if peer.ReplicationState.IsSyncingState() {
				return dmte.GuardResult{
					Blocked: true,
					Message: fmt.Sprintf("replica %s has peer %s in state %s",
						rc.name, peer.Name, peer.ReplicationState),
				}
			}
		}
	}
	return dmte.GuardResult{}
}

// guardBackingVolumesGrown blocks if any diskful member's backing volume has
// not yet grown to the target lower size. The target lower size is computed
// from rv.Spec.Size (the target usable size) plus DRBD metadata overhead.
// This guard ensures that all LVMs have finished growing before the resize
// transition starts, so the transition only waits for the fast DRBD resize.
func guardBackingVolumesGrown(gctx *globalContext) dmte.GuardResult {
	targetLowerSize := drbd_size.LowerVolumeSize(gctx.size)

	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.HasBackingVolume() || rc.rvr == nil {
			continue
		}
		bv := rc.rvr.Status.BackingVolume
		if bv == nil || bv.Size == nil {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf("backing volume size not yet reported on replica %s", rc.name),
			}
		}
		if bv.Size.Cmp(targetLowerSize) < 0 {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf("backing volume on replica %s not grown yet (%s, need >= %s)",
					rc.name, bv.Size.String(), targetLowerSize.String()),
			}
		}
	}
	return dmte.GuardResult{}
}
