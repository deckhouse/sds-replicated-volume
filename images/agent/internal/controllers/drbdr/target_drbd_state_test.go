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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func boolPtr(v bool) *bool { return &v }

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
	nodeID uint8
	bitmap *bool
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
func (s *stubActualPeer) Paths() []ActualPath     { return nil }

func TestComputePeerDeviceOptionsAction(t *testing.T) {
	tests := []struct {
		name          string
		iPeer         IntendedPeer
		aPeer         ActualPeer
		expectActions int
	}{
		{
			name: "diskless peer with bitmap=true emits action",
			iPeer: &stubIntendedPeer{
				nodeID:   1,
				peerType: v1alpha1.DRBDResourceTypeDiskless,
			},
			aPeer: &stubActualPeer{
				nodeID: 1,
				bitmap: boolPtr(true),
			},
			expectActions: 1,
		},
		{
			name: "diskless peer with bitmap=nil emits action",
			iPeer: &stubIntendedPeer{
				nodeID:   2,
				peerType: v1alpha1.DRBDResourceTypeDiskless,
			},
			aPeer: &stubActualPeer{
				nodeID: 2,
				bitmap: nil,
			},
			expectActions: 1,
		},
		{
			name: "diskless peer with bitmap=false emits no action",
			iPeer: &stubIntendedPeer{
				nodeID:   3,
				peerType: v1alpha1.DRBDResourceTypeDiskless,
			},
			aPeer: &stubActualPeer{
				nodeID: 3,
				bitmap: boolPtr(false),
			},
			expectActions: 0,
		},
		{
			name: "diskful peer with bitmap=true emits no action",
			iPeer: &stubIntendedPeer{
				nodeID:   4,
				peerType: v1alpha1.DRBDResourceTypeDiskful,
			},
			aPeer: &stubActualPeer{
				nodeID: 4,
				bitmap: boolPtr(true),
			},
			expectActions: 0,
		},
		{
			name: "diskful peer with bitmap=nil emits no action",
			iPeer: &stubIntendedPeer{
				nodeID:   5,
				peerType: v1alpha1.DRBDResourceTypeDiskful,
			},
			aPeer: &stubActualPeer{
				nodeID: 5,
				bitmap: nil,
			},
			expectActions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := computePeerDeviceOptionsAction("test-resource", tt.iPeer, tt.aPeer)

			if len(actions) != tt.expectActions {
				t.Fatalf("expected %d actions, got %d: %v", tt.expectActions, len(actions), actions)
			}

			if tt.expectActions > 0 {
				pdo, ok := actions[0].(PeerDeviceOptionsAction)
				if !ok {
					t.Fatalf("expected PeerDeviceOptionsAction, got %T", actions[0])
				}
				if pdo.ResourceName != "test-resource" {
					t.Errorf("expected resource name 'test-resource', got %q", pdo.ResourceName)
				}
				if pdo.PeerNodeID != tt.iPeer.NodeID() {
					t.Errorf("expected peer node ID %d, got %d", tt.iPeer.NodeID(), pdo.PeerNodeID)
				}
				if pdo.VolumeNr != 0 {
					t.Errorf("expected volume nr 0, got %d", pdo.VolumeNr)
				}
				if pdo.Bitmap == nil || *pdo.Bitmap != false {
					t.Errorf("expected bitmap=false, got %v", pdo.Bitmap)
				}
			}
		})
	}
}
