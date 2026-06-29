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

package suite

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupDiskfulToDisklessPeerCheck is an exploratory probe: it patches drbdrs[0]
// from Diskful to Diskless while drbdrs[1] stays Diskful, waits for both sides
// to settle, then runs non-fatal field-by-field checks to discover which
// status fields are NOT updated correctly across the transition.
//
// All findings are reported via e.Errorf so a single run surfaces every
// stale field on both sides. The patch is reverted on cleanup via
// SetupResourcePatch so subsequent subtests get a peered, diskful pair back.
func SetupDiskfulToDisklessPeerCheck(
	e envtesting.E,
	cl client.WithWatch,
	drbdrs []*v1alpha1.DRBDResource,
) {
	if len(drbdrs) < 2 {
		e.Fatalf("require: need at least 2 drbdrs, got %d", len(drbdrs))
	}

	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&drbdrConfiguredTimeout)

	peer0Name := drbdrs[0].Spec.NodeName
	peer0NodeID := drbdrs[0].Spec.NodeID
	peer1Name := drbdrs[1].Spec.NodeName
	peer1NodeID := drbdrs[1].Spec.NodeID

	// Start a watcher on the peer (replica 1) BEFORE patching replica 0,
	// so we don't miss the peer-side status update.
	peerWatchScope := e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration)
	defer peerWatchScope.Close()
	waitPeer := kubetesting.SetupResourceWatcher(peerWatchScope, cl, drbdrs[1])

	// Also watch replica 0 itself, so after the transition we can wait for its
	// own view of peer 1 to re-converge before asserting (see the wait below).
	selfWatchScope := e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration)
	defer selfWatchScope.Close()
	waitSelf := kubetesting.SetupResourceWatcher(selfWatchScope, cl, drbdrs[0])

	// Patch replica 0 to Diskless and wait for its own Configured to be terminal.
	drbdrs[0] = kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl,
		client.ObjectKey{Name: drbdrs[0].Name},
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Type = v1alpha1.DRBDResourceTypeDiskless
			d.Spec.LVMLogicalVolumeName = ""
			d.Spec.Size = nil
		},
		isDRBDRTerminal,
	)

	// Wait for replica 1 to observe peer 0 as Diskless (Type or DiskState).
	drbdrs[1] = waitPeer(peerWatchScope, drbdrs[1], func(d *v1alpha1.DRBDResource) bool {
		for i := range d.Status.Peers {
			p := &d.Status.Peers[i]
			if p.Name != peer0Name && p.NodeID != uint(peer0NodeID) {
				continue
			}
			if p.Type == v1alpha1.DRBDResourceTypeDiskless ||
				p.DiskState == v1alpha1.DiskStateDiskless {
				return true
			}
		}
		return false
	})

	// Wait for replica 0's own view of peer 1 to re-converge. Switching
	// replica 0 Diskful->Diskless is implemented as a full teardown+recreate of
	// its DRBD resource, which drops and re-establishes the peer connection;
	// during reconnect replica 0 transiently reports peer 1 as
	// Connecting/DUnknown before settling to Connected/UpToDate. Requiring
	// replica 0 to be Diskless here ensures we match the post-recreate settled
	// state, not the pre-transition snapshot, so the peer-side assertions below
	// check the converged value rather than the transient one.
	drbdrs[0] = waitSelf(selfWatchScope, drbdrs[0], func(d *v1alpha1.DRBDResource) bool {
		if d.Status.DiskState != v1alpha1.DiskStateDiskless {
			return false
		}
		for i := range d.Status.Peers {
			p := &d.Status.Peers[i]
			if p.Name != peer1Name && p.NodeID != uint(peer1NodeID) {
				continue
			}
			return p.ConnectionState == v1alpha1.ConnectionStateConnected &&
				p.DiskState == v1alpha1.DiskStateUpToDate
		}
		return false
	})

	// Dump for visibility — full status of both sides, post-transition.
	e.Logf("post-transition status of replica 0 (%q, was Diskful, now Diskless):\n%+v",
		drbdrs[0].Name, drbdrs[0].Status)
	e.Logf("post-transition status of replica 1 (%q, peer of replica 0):\n%+v",
		drbdrs[1].Name, drbdrs[1].Status)

	// ----- Local checks on replica 0 (the one that went diskless) -----
	r0 := drbdrs[0]
	if r0.Status.DiskState != v1alpha1.DiskStateDiskless {
		e.Errorf("replica0.status.diskState = %q, want %q",
			r0.Status.DiskState, v1alpha1.DiskStateDiskless)
	}
	if r0.Status.Size != nil {
		e.Errorf("replica0.status.size = %s, want nil (diskless)",
			r0.Status.Size.String())
	}
	if r0.Status.ActiveConfiguration == nil {
		e.Errorf("replica0.status.activeConfiguration is nil")
	} else {
		if r0.Status.ActiveConfiguration.Type != v1alpha1.DRBDResourceTypeDiskless {
			e.Errorf("replica0.status.activeConfiguration.type = %q, want Diskless",
				r0.Status.ActiveConfiguration.Type)
		}
		if r0.Status.ActiveConfiguration.LVMLogicalVolumeName != "" {
			e.Errorf("replica0.status.activeConfiguration.lvmLogicalVolumeName = %q, want empty",
				r0.Status.ActiveConfiguration.LVMLogicalVolumeName)
		}
		if len(r0.Status.ActiveConfiguration.LLVFinalizersToRelease) != 0 {
			e.Errorf("replica0.status.activeConfiguration.llvFinalizersToRelease = %v, want empty",
				r0.Status.ActiveConfiguration.LLVFinalizersToRelease)
		}
	}

	// ----- Peer-side checks on replica 1: how does it see peer 0? -----
	r1 := drbdrs[1]
	var p0FromR1 *v1alpha1.DRBDResourcePeerStatus
	for i := range r1.Status.Peers {
		p := &r1.Status.Peers[i]
		if p.Name == peer0Name || p.NodeID == uint(peer0NodeID) {
			p0FromR1 = p
			break
		}
	}
	if p0FromR1 == nil {
		e.Errorf("replica1.status.peers: no entry for peer 0 (%q, nodeID=%d)",
			peer0Name, peer0NodeID)
	} else {
		if p0FromR1.Type != v1alpha1.DRBDResourceTypeDiskless {
			e.Errorf("replica1.status.peers[0].type = %q, want Diskless",
				p0FromR1.Type)
		}
		if p0FromR1.DiskState != v1alpha1.DiskStateDiskless {
			e.Errorf("replica1.status.peers[0].diskState = %q, want Diskless",
				p0FromR1.DiskState)
		}
	}

	// ----- Peer-side checks on replica 0: how does it see peer 1? -----
	// Peer 1 stayed Diskful/UpToDate; check that replica 0 still reports it that way.
	var p1FromR0 *v1alpha1.DRBDResourcePeerStatus
	for i := range r0.Status.Peers {
		p := &r0.Status.Peers[i]
		if p.Name == peer1Name || p.NodeID == uint(peer1NodeID) {
			p1FromR0 = p
			break
		}
	}
	if p1FromR0 == nil {
		e.Errorf("replica0.status.peers: no entry for peer 1 (%q, nodeID=%d)",
			peer1Name, peer1NodeID)
	} else {
		if p1FromR0.Type != v1alpha1.DRBDResourceTypeDiskful {
			e.Errorf("replica0.status.peers[1].type = %q, want Diskful",
				p1FromR0.Type)
		}
		if p1FromR0.DiskState != v1alpha1.DiskStateUpToDate {
			e.Errorf("replica0.status.peers[1].diskState = %q, want UpToDate",
				p1FromR0.DiskState)
		}
	}
}
