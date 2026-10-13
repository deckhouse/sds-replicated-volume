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

package drbdr

import (
	"slices"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

func boolPtr(v bool) *bool { return &v }

// TestComputeDiskActions_DetachDrivenByIntendedDisk pins the invariant the LLV
// deletion fix relies on: a detach is emitted only when the intended disk
// diverges from the attached one. Keeping the attached disk as intended (what a
// deleting LLV now does) must not detach; clearing it (the controller-driven
// teardown) must.
//
// It also pins which of those detaches converts the device into an intentionally
// diskless client: only a retype to Diskless does. A detach that is followed by
// an attach (backing device swap) or by a teardown must stay a plain detach,
// while a retype must carry --diskless, or the kernel reports the device as
// unintentionally diskless and the D8DrbdDeviceIsUnintentionalDiskless alert fires.
func TestComputeDiskActions_DetachDrivenByIntendedDisk(t *testing.T) {
	const backingDisk = "/dev/test-vg/test-lv"

	attached := func() *actualState {
		return &actualState{
			status: &drbdutils.Resource{
				Name:    "sdsrv-x",
				Devices: []drbdutils.Device{{Volume: 0, Minor: 1000, DiskState: "UpToDate"}},
			},
			show: &drbdutils.ShowResource{
				Resource: "sdsrv-x",
				ThisHost: drbdutils.ShowThisHost{
					Volumes: []drbdutils.ShowVolume{{VolumeNr: 0, DeviceMinor: 1000, BackingDisk: backingDisk}},
				},
			},
		}
	}

	// detached is the state of a device that has already been detached: the minor
	// exists, but no backing disk is attached to it.
	detached := func() *actualState {
		return &actualState{
			status: &drbdutils.Resource{
				Name:    "sdsrv-x",
				Devices: []drbdutils.Device{{Volume: 0, Minor: 1000, DiskState: "Diskless"}},
			},
			show: &drbdutils.ShowResource{
				Resource: "sdsrv-x",
				ThisHost: drbdutils.ShowThisHost{
					Volumes: []drbdutils.ShowVolume{{VolumeNr: 0, DeviceMinor: 1000}},
				},
			},
		}
	}

	diskful := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{Type: v1alpha1.DRBDResourceTypeDiskful}}
	diskless := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{Type: v1alpha1.DRBDResourceTypeDiskless}}
	minor := uint(1000)

	t.Run("intended disk equals attached - no detach", func(t *testing.T) {
		iState := computeIntendedDRBDState(diskful, backingDisk, true)
		actions := computeDiskActions(&minor, iState, attached())
		if _, ok := detachAction(actions); ok {
			t.Errorf("unexpected DetachAction when intended disk matches attached: %v", actions)
		}
	})

	t.Run("intended disk empty while attached - detaches", func(t *testing.T) {
		iState := computeIntendedDRBDState(diskful, "", true)
		actions := computeDiskActions(&minor, iState, attached())
		action, ok := detachAction(actions)
		if !ok {
			t.Fatalf("expected DetachAction when intended disk is empty but disk is attached: %v", actions)
		}
		// Teardown of a Diskful resource: the device is going away, not becoming a client.
		if action.IntentionalDiskless {
			t.Errorf("DetachAction.IntentionalDiskless = true for a Diskful teardown, want false: %v", actions)
		}
	})

	t.Run("retype to diskless while attached - intentional diskless detach", func(t *testing.T) {
		iState := computeIntendedDRBDState(diskless, "", true)
		actions := computeDiskActions(&minor, iState, attached())
		if len(actions) != 1 {
			t.Fatalf("computeDiskActions() len = %d, want exactly one detach: %v", len(actions), actions)
		}
		action, ok := detachAction(actions)
		if !ok {
			t.Fatalf("expected DetachAction on retype to Diskless: %v", actions)
		}
		if !action.IntentionalDiskless {
			t.Errorf("DetachAction.IntentionalDiskless = false on retype to Diskless, want true: %v", actions)
		}
	})

	t.Run("diskful backing device swap - plain detach", func(t *testing.T) {
		iState := computeIntendedDRBDState(diskful, "/dev/test-vg/other-lv", true)
		actions := computeDiskActions(&minor, iState, attached())
		action, ok := detachAction(actions)
		if !ok {
			t.Fatalf("expected DetachAction when the intended backing device changed: %v", actions)
		}
		// An attach to the new device follows, so the device must not be marked diskless.
		if action.IntentionalDiskless {
			t.Errorf("DetachAction.IntentionalDiskless = true for a backing device swap, want false: %v", actions)
		}
	})

	// A repeated detach on an already diskless device does not heal the flag: the
	// kernel resets intentional_diskless back to false when the state transition
	// fails with SS_IS_DISKLESS. Nothing may be emitted here.
	t.Run("intended diskless with no attached disk - no actions", func(t *testing.T) {
		iState := computeIntendedDRBDState(diskless, "", true)
		actions := computeDiskActions(&minor, iState, detached())
		if len(actions) != 0 {
			t.Errorf("computeDiskActions() = %v, want no actions for an already detached device", actions)
		}
	})
}

func detachAction(actions DRBDActions) (DetachAction, bool) {
	for _, a := range actions {
		if d, ok := a.(DetachAction); ok {
			return d, true
		}
	}
	return DetachAction{}, false
}

type stubIntendedPeer struct {
	name     string
	nodeID   uint8
	peerType v1alpha1.DRBDResourceType
}

func (s *stubIntendedPeer) Name() string                              { return s.name }
func (s *stubIntendedPeer) NodeID() uint8                             { return s.nodeID }
func (s *stubIntendedPeer) Type() v1alpha1.DRBDResourceType           { return s.peerType }
func (s *stubIntendedPeer) Protocol() v1alpha1.DRBDProtocol           { return "C" }
func (s *stubIntendedPeer) SharedSecret() string                      { return "" }
func (s *stubIntendedPeer) SharedSecretAlg() v1alpha1.SharedSecretAlg { return "" }
func (s *stubIntendedPeer) AllowRemoteRead() bool                     { return false }
func (s *stubIntendedPeer) RRConflict() string                        { return "retry-connect" }
func (s *stubIntendedPeer) VerifyAlg() string                         { return "" }
func (s *stubIntendedPeer) Paths() []IntendedPath                     { return nil }

type stubActualPeer struct {
	nodeID       uint8
	bitmap       *bool
	cPlanAhead   string
	cDelayTarget string
	cFillTarget  string
	cMaxRate     string
	cMinRate     string
}

func (s *stubActualPeer) NodeID() uint8           { return s.nodeID }
func (s *stubActualPeer) Name() string            { return "" }
func (s *stubActualPeer) ConnectionState() string { return "Connected" }
func (s *stubActualPeer) PeerDiskState() string   { return "" }
func (s *stubActualPeer) Protocol() string        { return "C" }
func (s *stubActualPeer) SharedSecret() string    { return "" }
func (s *stubActualPeer) SharedSecretAlg() string { return "" }
func (s *stubActualPeer) AllowTwoPrimaries() bool { return false }
func (s *stubActualPeer) AllowRemoteRead() bool   { return false }
func (s *stubActualPeer) VerifyAlg() string       { return "" }
func (s *stubActualPeer) Bitmap() *bool           { return s.bitmap }
func (s *stubActualPeer) CPlanAhead() string      { return s.cPlanAhead }
func (s *stubActualPeer) CDelayTarget() string    { return s.cDelayTarget }
func (s *stubActualPeer) CFillTarget() string     { return s.cFillTarget }
func (s *stubActualPeer) CMaxRate() string        { return s.cMaxRate }
func (s *stubActualPeer) CMinRate() string        { return s.cMinRate }
func (s *stubActualPeer) Paths() []ActualPath     { return nil }

func stubActualPeerWithDefaults(nodeID uint8, bitmap *bool) *stubActualPeer {
	return &stubActualPeer{
		nodeID:       nodeID,
		bitmap:       bitmap,
		cPlanAhead:   DefaultCPlanAhead,
		cDelayTarget: DefaultCDelayTarget,
		cFillTarget:  DefaultCFillTarget,
		cMaxRate:     DefaultCMaxRate,
		cMinRate:     DefaultCMinRate,
	}
}

func TestComputePeerDeviceOptionsAction(t *testing.T) {
	t.Run("all defaults match, diskful — no action", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 1, peerType: v1alpha1.DRBDResourceTypeDiskful,
		}, stubActualPeerWithDefaults(1, boolPtr(true)))
		if len(actions) != 0 {
			t.Fatalf("expected 0 actions, got %d: %v", len(actions), actions)
		}
	})

	t.Run("all defaults match, diskless bitmap=false — no action", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 2, peerType: v1alpha1.DRBDResourceTypeDiskless,
		}, stubActualPeerWithDefaults(2, boolPtr(false)))
		if len(actions) != 0 {
			t.Fatalf("expected 0 actions, got %d: %v", len(actions), actions)
		}
	})

	t.Run("diskless bitmap=true — action with bitmap + no resync fields", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 3, peerType: v1alpha1.DRBDResourceTypeDiskless,
		}, stubActualPeerWithDefaults(3, boolPtr(true)))
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.Bitmap == nil || *pdo.Bitmap != false {
			t.Errorf("expected bitmap=false, got %v", pdo.Bitmap)
		}
		if pdo.CPlanAhead != nil {
			t.Errorf("expected CPlanAhead nil (already correct), got %v", *pdo.CPlanAhead)
		}
	})

	t.Run("diskful with non-default c-max-rate — action with only c-max-rate", func(t *testing.T) {
		aPeer := stubActualPeerWithDefaults(4, boolPtr(true))
		aPeer.cMaxRate = "102400k"
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 4, peerType: v1alpha1.DRBDResourceTypeDiskful,
		}, aPeer)
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.CMaxRate == nil || *pdo.CMaxRate != DefaultCMaxRate {
			t.Errorf("expected CMaxRate=%q, got %v", DefaultCMaxRate, pdo.CMaxRate)
		}
		if pdo.Bitmap != nil {
			t.Errorf("expected Bitmap nil (diskful), got %v", *pdo.Bitmap)
		}
		if pdo.CPlanAhead != nil {
			t.Errorf("expected CPlanAhead nil (already correct), got %v", *pdo.CPlanAhead)
		}
	})

	t.Run("all c-* fields non-default — action with all 5 fields", func(t *testing.T) {
		aPeer := &stubActualPeer{nodeID: 5, bitmap: boolPtr(true)}
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 5, peerType: v1alpha1.DRBDResourceTypeDiskful,
		}, aPeer)
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.CPlanAhead == nil {
			t.Error("expected CPlanAhead to be set")
		}
		if pdo.CDelayTarget == nil {
			t.Error("expected CDelayTarget to be set")
		}
		if pdo.CFillTarget == nil {
			t.Error("expected CFillTarget to be set")
		}
		if pdo.CMaxRate == nil {
			t.Error("expected CMaxRate to be set")
		}
		if pdo.CMinRate == nil {
			t.Error("expected CMinRate to be set")
		}
	})

	t.Run("diskless bitmap=nil, c-* defaults match — action for bitmap only", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 6, peerType: v1alpha1.DRBDResourceTypeDiskless,
		}, stubActualPeerWithDefaults(6, nil))
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.Bitmap == nil || *pdo.Bitmap != false {
			t.Errorf("expected bitmap=false, got %v", pdo.Bitmap)
		}
	})

	// Main regression case for the diskless -> diskful transition (B-1): a peer that
	// was typed Diskless during an Access stage keeps bitmap=no on the kernel, and
	// once it becomes Diskful the bitmap must be turned back on, otherwise the
	// kernel refuses its disk attach.
	t.Run("diskful bitmap=false — action re-enables bitmap", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 7, peerType: v1alpha1.DRBDResourceTypeDiskful,
		}, stubActualPeerWithDefaults(7, boolPtr(false)))
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.Bitmap == nil || *pdo.Bitmap != true {
			t.Errorf("expected bitmap=true, got %v", pdo.Bitmap)
		}
		if pdo.CPlanAhead != nil {
			t.Errorf("expected CPlanAhead nil (already correct), got %v", *pdo.CPlanAhead)
		}
	})

	t.Run("diskful bitmap=nil — action re-enables bitmap", func(t *testing.T) {
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 8, peerType: v1alpha1.DRBDResourceTypeDiskful,
		}, stubActualPeerWithDefaults(8, nil))
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.Bitmap == nil || *pdo.Bitmap != true {
			t.Errorf("expected bitmap=true, got %v", pdo.Bitmap)
		}
	})

	t.Run("diskful bitmap re-enabled — next pass is a no-op", func(t *testing.T) {
		iPeer := &stubIntendedPeer{nodeID: 9, peerType: v1alpha1.DRBDResourceTypeDiskful}
		aPeer := stubActualPeerWithDefaults(9, boolPtr(false))

		actions := computePeerDeviceOptionsAction("res", iPeer, aPeer)
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		// Apply the computed action to the actual state and recompute.
		aPeer.bitmap = actions[0].(PeerDeviceOptionsAction).Bitmap

		if actions := computePeerDeviceOptionsAction("res", iPeer, aPeer); len(actions) != 0 {
			t.Fatalf("expected 0 actions after applying bitmap, got %d: %v", len(actions), actions)
		}
	})

	t.Run("diskless with non-default c-max-rate — action keeps bitmap off untouched", func(t *testing.T) {
		aPeer := stubActualPeerWithDefaults(10, boolPtr(false))
		aPeer.cMaxRate = "102400k"
		actions := computePeerDeviceOptionsAction("res", &stubIntendedPeer{
			nodeID: 10, peerType: v1alpha1.DRBDResourceTypeDiskless,
		}, aPeer)
		if len(actions) != 1 {
			t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
		}
		pdo := actions[0].(PeerDeviceOptionsAction)
		if pdo.CMaxRate == nil || *pdo.CMaxRate != DefaultCMaxRate {
			t.Errorf("expected CMaxRate=%q, got %v", DefaultCMaxRate, pdo.CMaxRate)
		}
		if pdo.Bitmap != nil {
			t.Errorf("expected Bitmap nil (already off for diskless), got %v", *pdo.Bitmap)
		}
	})
}

// TestComputeBringUpActions_QuorumOrder pins where a quorum change goes relative
// to the peer options. Changing q makes the kernel recount votes at once, and
// turning a peer's bitmap on or off does not, so a raise must follow the peer
// that brings the new vote and a lowering must precede the peer that loses it;
// in the other order the recount meets one vote too few and suspends I/O.
func TestComputeBringUpActions_QuorumOrder(t *testing.T) {
	const backingDisk = "/dev/test-vg/test-lv"

	// existing is a resource that is up, attached and connected to peer 1.
	existing := func(quorum, qmr string, peerBitmap bool) *actualState {
		return &actualState{
			status: &drbdutils.Resource{
				Name:        "sdsrv-x",
				Devices:     []drbdutils.Device{{Volume: 0, Minor: 1000, DiskState: "UpToDate"}},
				Connections: []drbdutils.Connection{{PeerNodeID: 1, ConnectionState: "Connected"}},
			},
			show: &drbdutils.ShowResource{
				Resource: "sdsrv-x",
				Options:  drbdutils.ShowOptions{Quorum: quorum, QuorumMinimumRedundancy: qmr},
				ThisHost: drbdutils.ShowThisHost{
					Volumes: []drbdutils.ShowVolume{{VolumeNr: 0, DeviceMinor: 1000, BackingDisk: backingDisk}},
				},
				Connections: []drbdutils.ShowConnection{{
					PeerNodeID: 1,
					Volumes:    []drbdutils.ShowConnectionVolume{{Disk: drbdutils.ShowConnectionVolumeDisk{Bitmap: peerBitmap}}},
				}},
			},
		}
	}

	intended := func(quorum, qmr byte, peerType v1alpha1.DRBDResourceType) IntendedDRBDState {
		return computeIntendedDRBDState(&v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{
			Type:                    v1alpha1.DRBDResourceTypeDiskful,
			Quorum:                  quorum,
			QuorumMinimumRedundancy: qmr,
			Peers:                   []v1alpha1.DRBDResourcePeer{{Name: "peer", NodeID: 1, Type: peerType}},
		}}, backingDisk, true)
	}

	t.Run("odd->even join: the new vote is in place before q goes up", func(t *testing.T) {
		actions := computeBringUpActions(intended(2, 1, v1alpha1.DRBDResourceTypeDiskful), existing("1", "1", false))

		bitmapAt := peerBitmapIndex(actions, true)
		quorumAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.Quorum != nil })
		if bitmapAt < 0 || quorumAt < 0 {
			t.Fatalf("want both bitmap=yes and quorum=2 among %v", actions)
		}
		if quorumAt < bitmapAt {
			t.Errorf("quorum raised at #%d before the peer's bitmap is on at #%d: %v", quorumAt, bitmapAt, actions)
		}
		if q := *actions[quorumAt].(ResourceOptionsAction).Quorum; q != 2 {
			t.Errorf("quorum = %d, want 2", q)
		}
	})

	t.Run("voter leaving: q goes down before the vote is gone", func(t *testing.T) {
		actions := computeBringUpActions(intended(1, 1, v1alpha1.DRBDResourceTypeDiskless), existing("2", "1", true))

		bitmapAt := peerBitmapIndex(actions, false)
		quorumAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.Quorum != nil })
		if bitmapAt < 0 || quorumAt < 0 {
			t.Fatalf("want both bitmap=no and quorum=1 among %v", actions)
		}
		if quorumAt > bitmapAt {
			t.Errorf("quorum lowered at #%d after the peer's bitmap is off at #%d: %v", quorumAt, bitmapAt, actions)
		}
	})

	t.Run("qmr raise waits too, the other options do not", func(t *testing.T) {
		actions := computeBringUpActions(intended(2, 2, v1alpha1.DRBDResourceTypeDiskful), existing("2", "1", true))

		qmrAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.QuorumMinimumRedundancy != nil })
		otherAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.OnNoQuorum != "" })
		if qmrAt < 0 || otherAt < 0 {
			t.Fatalf("want both the qmr raise and the on-no-quorum update among %v", actions)
		}
		if qmrAt <= otherAt {
			t.Errorf("qmr raise at #%d must be a separate, later call than the other options at #%d: %v", qmrAt, otherAt, actions)
		}
	})

	t.Run("q up and qmr down: the lowering is early, the raise late", func(t *testing.T) {
		actions := computeBringUpActions(intended(2, 1, v1alpha1.DRBDResourceTypeDiskful), existing("1", "2", false))

		qmrAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.QuorumMinimumRedundancy != nil })
		quorumAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.Quorum != nil })
		if qmrAt < 0 || quorumAt < 0 || qmrAt >= quorumAt {
			t.Errorf("want the qmr lowering (#%d) before the quorum raise (#%d): %v", qmrAt, quorumAt, actions)
		}
	})

	// threeVoters is node 0 of three UpToDate voters, with peer 1 Connected and
	// peer 2 in peer2State, and peer 3, an Access replica: Connected, diskless,
	// no bitmap.
	threeVoters := func(peer2State, peer2Disk string) *actualState {
		connection := func(id int, state, disk string) drbdutils.Connection {
			return drbdutils.Connection{PeerNodeID: id, ConnectionState: state, PeerDevices: []drbdutils.PeerDevice{{PeerDiskState: disk}}}
		}
		showConnection := func(id int, bitmap bool) drbdutils.ShowConnection {
			return drbdutils.ShowConnection{
				PeerNodeID: id,
				Net:        drbdutils.ShowNet{AllowRemoteRead: true},
				Volumes:    []drbdutils.ShowConnectionVolume{{Disk: drbdutils.ShowConnectionVolumeDisk{Bitmap: bitmap}}},
			}
		}
		return &actualState{
			status: &drbdutils.Resource{
				Name:    "sdsrv-x",
				Devices: []drbdutils.Device{{Volume: 0, Minor: 1000, DiskState: "UpToDate"}},
				Connections: []drbdutils.Connection{
					connection(1, "Connected", "UpToDate"),
					connection(2, peer2State, peer2Disk),
					connection(3, "Connected", "Diskless"),
				},
			},
			show: &drbdutils.ShowResource{
				Resource: "sdsrv-x",
				Options:  drbdutils.ShowOptions{Quorum: "2", QuorumMinimumRedundancy: "1"},
				ThisHost: drbdutils.ShowThisHost{
					Volumes: []drbdutils.ShowVolume{{VolumeNr: 0, DeviceMinor: 1000, BackingDisk: backingDisk}},
				},
				Connections: []drbdutils.ShowConnection{showConnection(1, true), showConnection(2, true), showConnection(3, false)},
			},
		}
	}

	// fourVoters makes peer 3 the fourth voter and raises q to 3.
	fourVoters := computeIntendedDRBDState(&v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{
		Type:                    v1alpha1.DRBDResourceTypeDiskful,
		Quorum:                  3,
		QuorumMinimumRedundancy: 1,
		Peers: []v1alpha1.DRBDResourcePeer{
			{Name: "peer-1", NodeID: 1, Type: v1alpha1.DRBDResourceTypeDiskful, AllowRemoteRead: true},
			{Name: "peer-2", NodeID: 2, Type: v1alpha1.DRBDResourceTypeDiskful, AllowRemoteRead: true},
			{Name: "peer-3", NodeID: 3, Type: v1alpha1.DRBDResourceTypeDiskful, AllowRemoteRead: true},
		},
	}}, backingDisk, true)

	t.Run("3 -> 4 voters: q goes up before the new vote, the old votes already cover it", func(t *testing.T) {
		actions := computeBringUpActions(fourVoters, threeVoters("Connected", "UpToDate"))

		bitmapAt := peerBitmapIndex(actions, true)
		quorumAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.Quorum != nil })
		if bitmapAt < 0 || quorumAt < 0 {
			t.Fatalf("want both bitmap=yes and quorum=3 among %v", actions)
		}
		if quorumAt > bitmapAt {
			t.Errorf("peer's bitmap on at #%d before quorum raised at #%d — 4 voters at q=2: %v", bitmapAt, quorumAt, actions)
		}
	})

	t.Run("3 -> 4 voters with an old voter away: the raise waits for the new vote", func(t *testing.T) {
		actions := computeBringUpActions(fourVoters, threeVoters("Connecting", "DUnknown"))

		bitmapAt := peerBitmapIndex(actions, true)
		quorumAt := resourceOptionsIndex(actions, func(a ResourceOptionsAction) bool { return a.Quorum != nil })
		if bitmapAt < 0 || quorumAt < 0 {
			t.Fatalf("want both bitmap=yes and quorum=3 among %v", actions)
		}
		if quorumAt < bitmapAt {
			t.Errorf("quorum raised at #%d before the peer's bitmap is on at #%d, with 2 votes counted: %v", quorumAt, bitmapAt, actions)
		}
	})

	t.Run("new resource: the whole option set right after new-resource", func(t *testing.T) {
		actions := computeBringUpActions(intended(2, 2, v1alpha1.DRBDResourceTypeDiskful), &actualState{})

		var options []int
		for i, a := range actions {
			if _, ok := a.(ResourceOptionsAction); ok {
				options = append(options, i)
			}
		}
		if len(options) != 1 || options[0] != 1 {
			t.Fatalf("want exactly one resource-options call, right after new-resource: %v", actions)
		}
		o := actions[1].(ResourceOptionsAction)
		if o.Quorum == nil || *o.Quorum != 2 || o.QuorumMinimumRedundancy == nil || *o.QuorumMinimumRedundancy != 2 {
			t.Errorf("new resource options = %+v, want quorum 2 and qmr 2", o)
		}
	})
}

func TestQuorumRaised(t *testing.T) {
	for _, tc := range []struct {
		intended uint
		actual   string
		want     bool
	}{
		{2, "1", true},
		{2, "2", false},
		{1, "2", false},
		{1, "off", true},
		{0, "off", false},
		{0, "2", false},
		{2, "majority", false},
		{2, "", false},
	} {
		if got := quorumRaised(tc.intended, tc.actual); got != tc.want {
			t.Errorf("quorumRaised(%d, %q) = %v, want %v", tc.intended, tc.actual, got, tc.want)
		}
	}
}

func TestCountedVotes(t *testing.T) {
	const backingDisk = "/dev/test-vg/test-lv"

	type peer struct {
		state, disk     string
		bitmap, allowRR bool
	}
	votes := func(localDisk string, nonVoting bool, p peer) uint {
		aState := &actualState{
			status: &drbdutils.Resource{
				Devices:     []drbdutils.Device{{Volume: 0, DiskState: localDisk}},
				Connections: []drbdutils.Connection{{PeerNodeID: 1, ConnectionState: p.state, PeerDevices: []drbdutils.PeerDevice{{PeerDiskState: p.disk}}}},
			},
			show: &drbdutils.ShowResource{
				ThisHost: drbdutils.ShowThisHost{Volumes: []drbdutils.ShowVolume{{
					VolumeNr: 0, BackingDisk: backingDisk, Disk: drbdutils.ShowVolumeDisk{NonVoting: nonVoting},
				}}},
				Connections: []drbdutils.ShowConnection{{
					PeerNodeID: 1,
					Net:        drbdutils.ShowNet{AllowRemoteRead: p.allowRR},
					Volumes:    []drbdutils.ShowConnectionVolume{{Disk: drbdutils.ShowConnectionVolumeDisk{Bitmap: p.bitmap}}},
				}},
			},
		}
		return countedVotes(aState)
	}

	for _, tc := range []struct {
		name      string
		localDisk string
		nonVoting bool
		peer      peer
		want      uint
	}{
		{"diskful peer", "UpToDate", false, peer{"Connected", "UpToDate", true, true}, 2},
		{"resyncing peer", "UpToDate", false, peer{"Connected", "Inconsistent", true, true}, 2},
		{"diskless peer with a bitmap (D∅)", "UpToDate", false, peer{"Connected", "Diskless", true, true}, 2},
		{"diskless peer without a bitmap (A)", "UpToDate", false, peer{"Connected", "Diskless", false, true}, 1},
		{"peer not connected", "UpToDate", false, peer{"Connecting", "DUnknown", true, true}, 1},
		{"peer with allow-remote-read off", "UpToDate", false, peer{"Connected", "UpToDate", true, false}, 1},
		{"non-voting local disk", "UpToDate", true, peer{"Connected", "UpToDate", true, true}, 1},
		{"diskless node: a diskless peer is no vote, bitmap or not", "Diskless", false, peer{"Connected", "Diskless", true, true}, 0},
		{"diskless node: a diskful peer is", "Diskless", false, peer{"Connected", "UpToDate", true, true}, 1},
	} {
		if got := votes(tc.localDisk, tc.nonVoting, tc.peer); got != tc.want {
			t.Errorf("%s: countedVotes = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func peerBitmapIndex(actions DRBDActions, bitmap bool) int {
	return slices.IndexFunc(actions, func(a DRBDAction) bool {
		pdo, ok := a.(PeerDeviceOptionsAction)
		return ok && pdo.Bitmap != nil && *pdo.Bitmap == bitmap
	})
}

func resourceOptionsIndex(actions DRBDActions, match func(ResourceOptionsAction) bool) int {
	return slices.IndexFunc(actions, func(a DRBDAction) bool {
		o, ok := a.(ResourceOptionsAction)
		return ok && match(o)
	})
}
