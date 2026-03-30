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
	"testing"
)

type stubMetadata struct {
	hasMetadata bool
	diskUUID    string
}

func (s stubMetadata) HasMetadata() bool      { return s.hasMetadata }
func (s stubMetadata) DiskDeviceUUID() string { return s.diskUUID }

func uintPtr(v uint) *uint { return &v }

func TestComputeAttachActions_PreserveMetadata(t *testing.T) {
	minor := uintPtr(0)
	dev := "/dev/vg/snap"

	tests := []struct {
		name             string
		metadata         stubMetadata
		statusUUID       string
		preserveMetadata bool
		wantFirst        string
	}{
		{
			name:             "preserve=true, metadata exists -> ApplyAL only",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "",
			preserveMetadata: true,
			wantFirst:        "ApplyALAction",
		},
		{
			name:             "preserve=true, no metadata -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: false},
			statusUUID:       "",
			preserveMetadata: true,
			wantFirst:        "CreateMetadataAction",
		},
		{
			name:             "preserve=false, metadata exists, no statusUUID -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "",
			preserveMetadata: false,
			wantFirst:        "CreateMetadataAction",
		},
		{
			name:             "preserve=false, metadata exists, matching statusUUID -> ApplyAL",
			metadata:         stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"},
			statusUUID:       "ABCD1234ABCD1234",
			preserveMetadata: false,
			wantFirst:        "ApplyALAction",
		},
		{
			name:             "preserve=false, no metadata, no statusUUID -> CreateMetadata",
			metadata:         stubMetadata{hasMetadata: false},
			statusUUID:       "",
			preserveMetadata: false,
			wantFirst:        "CreateMetadataAction",
		},
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actions := computeAttachActions(tc.metadata, tc.statusUUID, minor, dev, tc.preserveMetadata)

			if len(actions) == 0 {
				t.Fatal("got 0 actions, want at least 1")
			}

			var firstType string
			switch actions[0].(type) {
			case ApplyALAction:
				firstType = "ApplyALAction"
			case CreateMetadataAction:
				firstType = "CreateMetadataAction"
			default:
				firstType = "unknown"
			}

			if firstType != tc.wantFirst {
				t.Errorf("first action = %s, want %s", firstType, tc.wantFirst)
			}

			last := actions[len(actions)-1]
			if _, ok := last.(AttachAction); !ok {
				t.Errorf("last action = %T, want AttachAction", last)
			}
		})
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
}

func TestComputeAttachActions_PreserveMetadata_NoCreateMdNoWriteUUID(t *testing.T) {
	minor := uintPtr(0)
	dev := "/dev/vg/snap"
	meta := stubMetadata{hasMetadata: true, diskUUID: "ABCD1234ABCD1234"}

	actions := computeAttachActions(meta, "", minor, dev, true)

	for _, a := range actions {
		switch a.(type) {
		case CreateMetadataAction:
			t.Error("preserve=true with metadata must not produce CreateMetadataAction")
		case WriteDeviceUUIDAction:
			t.Error("preserve=true with metadata must not produce WriteDeviceUUIDAction")
		}
	}

	if len(actions) != 2 {
		t.Errorf("len(actions) = %d, want 2 (ApplyAL + Attach)", len(actions))
	}
}
