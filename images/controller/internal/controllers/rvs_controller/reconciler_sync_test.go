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
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func drbdr(_ string, nodeID uint8, diskState v1alpha1.DiskState, addresses []v1alpha1.DRBDResourceAddressStatus, peers []v1alpha1.DRBDResourcePeerStatus) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		Spec: v1alpha1.DRBDResourceSpec{
			NodeID: nodeID,
			Peers:  specPeersFromStatus(peers),
		},
		Status: v1alpha1.DRBDResourceStatus{
			DiskState: diskState,
			Addresses: addresses,
			Peers:     peers,
		},
	}
}

func specPeersFromStatus(statusPeers []v1alpha1.DRBDResourcePeerStatus) []v1alpha1.DRBDResourcePeer {
	if len(statusPeers) == 0 {
		return nil
	}
	out := make([]v1alpha1.DRBDResourcePeer, len(statusPeers))
	for i, p := range statusPeers {
		out[i] = v1alpha1.DRBDResourcePeer{Name: p.Name, NodeID: uint8(p.NodeID)}
	}
	return out
}

func withName(d *v1alpha1.DRBDResource, name string) *v1alpha1.DRBDResource {
	d.Name = name
	return d
}

func addr(network, ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
	return v1alpha1.DRBDResourceAddressStatus{
		SystemNetworkName: network,
		Address:           v1alpha1.DRBDAddress{IPv4: ip, Port: port},
	}
}

func peerStatus(name string, nodeID uint, diskState v1alpha1.DiskState) v1alpha1.DRBDResourcePeerStatus {
	return v1alpha1.DRBDResourcePeerStatus{
		Name:      name,
		NodeID:    nodeID,
		DiskState: diskState,
	}
}

func TestAllGone(t *testing.T) {
	if !allGone(nil) {
		t.Error("nil slice: want true")
	}
	if !allGone([]*v1alpha1.DRBDResource{}) {
		t.Error("empty slice: want true")
	}
	if allGone([]*v1alpha1.DRBDResource{{}}) {
		t.Error("non-empty slice: want false")
	}
}

func TestAllHaveAddresses(t *testing.T) {
	noAddr := withName(drbdr("a", 0, "", nil, nil), "a")
	withAddr := withName(drbdr("b", 1, "", []v1alpha1.DRBDResourceAddressStatus{addr("net", "1.2.3.4", 7000)}, nil), "b")

	if allHaveAddresses([]*v1alpha1.DRBDResource{noAddr}) {
		t.Error("no addresses: want false")
	}
	if !allHaveAddresses([]*v1alpha1.DRBDResource{withAddr}) {
		t.Error("with address: want true")
	}
	if allHaveAddresses([]*v1alpha1.DRBDResource{withAddr, noAddr}) {
		t.Error("mixed: want false")
	}
}

func TestAllPeersWired(t *testing.T) {
	noPeers := &v1alpha1.DRBDResource{}
	withPeers := &v1alpha1.DRBDResource{
		Spec: v1alpha1.DRBDResourceSpec{
			Peers: []v1alpha1.DRBDResourcePeer{{Name: "peer-0", NodeID: 0}},
		},
	}

	if allPeersWired([]*v1alpha1.DRBDResource{noPeers}) {
		t.Error("no peers: want false")
	}
	if !allPeersWired([]*v1alpha1.DRBDResource{withPeers}) {
		t.Error("with peers: want true")
	}
}

func TestIsSyncComplete(t *testing.T) {
	upToDate := v1alpha1.DiskStateUpToDate
	inconsistent := v1alpha1.DiskState("Inconsistent")

	tests := []struct {
		name string
		in   []*v1alpha1.DRBDResource
		want bool
	}{
		{
			name: "all UpToDate, peers UpToDate",
			in: []*v1alpha1.DRBDResource{
				drbdr("a", 0, upToDate, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("b", 1, upToDate)}),
				drbdr("b", 1, upToDate, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("a", 0, upToDate)}),
			},
			want: true,
		},
		{
			name: "self not UpToDate",
			in: []*v1alpha1.DRBDResource{
				drbdr("a", 0, inconsistent, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("b", 1, upToDate)}),
				drbdr("b", 1, upToDate, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("a", 0, inconsistent)}),
			},
			want: false,
		},
		{
			name: "peer not UpToDate",
			in: []*v1alpha1.DRBDResource{
				drbdr("a", 0, upToDate, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("b", 1, inconsistent)}),
				drbdr("b", 1, upToDate, nil, []v1alpha1.DRBDResourcePeerStatus{peerStatus("a", 0, upToDate)}),
			},
			want: false,
		},
		{
			name: "no peers reported yet",
			in: []*v1alpha1.DRBDResource{
				drbdr("a", 0, upToDate, nil, nil),
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSyncComplete(tc.in)
			if got != tc.want {
				t.Errorf("isSyncComplete() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildSyncPeers(t *testing.T) {
	a := withName(drbdr("a", 0, "", []v1alpha1.DRBDResourceAddressStatus{addr("Internal", "10.0.0.1", 7000)}, nil), "a")
	b := withName(drbdr("b", 1, "", []v1alpha1.DRBDResourceAddressStatus{addr("Internal", "10.0.0.2", 7001)}, nil), "b")
	c := withName(drbdr("c", 2, "", []v1alpha1.DRBDResourceAddressStatus{addr("Internal", "10.0.0.3", 7002)}, nil), "c")

	syncDRBDRs := []*v1alpha1.DRBDResource{a, b, c}
	secret := "test-secret"

	peers := buildSyncPeers(a, syncDRBDRs, secret)

	if len(peers) != 2 {
		t.Fatalf("len(peers) = %d, want 2", len(peers))
	}

	if peers[0].Name != "b" {
		t.Errorf("peers[0].Name = %q, want %q", peers[0].Name, "b")
	}
	if peers[0].NodeID != 1 {
		t.Errorf("peers[0].NodeID = %d, want 1", peers[0].NodeID)
	}
	if peers[0].SharedSecret != secret {
		t.Errorf("peers[0].SharedSecret = %q, want %q", peers[0].SharedSecret, secret)
	}
	if len(peers[0].Paths) != 1 || peers[0].Paths[0].Address.IPv4 != "10.0.0.2" {
		t.Errorf("peers[0].Paths unexpected: %+v", peers[0].Paths)
	}

	if peers[1].Name != "c" {
		t.Errorf("peers[1].Name = %q, want %q", peers[1].Name, "c")
	}
	if peers[1].NodeID != 2 {
		t.Errorf("peers[1].NodeID = %d, want 2", peers[1].NodeID)
	}
}

func TestPeersEqual(t *testing.T) {
	mkPeer := func(name string, nodeID uint8, secret string, ip string, port uint) v1alpha1.DRBDResourcePeer {
		return v1alpha1.DRBDResourcePeer{
			Name:         name,
			NodeID:       nodeID,
			SharedSecret: secret,
			Paths: []v1alpha1.DRBDResourcePath{
				{SystemNetworkName: "Internal", Address: v1alpha1.DRBDAddress{IPv4: ip, Port: port}},
			},
		}
	}

	a := []v1alpha1.DRBDResourcePeer{mkPeer("a", 0, "s", "1.2.3.4", 7000)}
	aCopy := []v1alpha1.DRBDResourcePeer{mkPeer("a", 0, "s", "1.2.3.4", 7000)}
	b := []v1alpha1.DRBDResourcePeer{mkPeer("b", 1, "s", "1.2.3.5", 7001)}

	if !peersEqual(a, aCopy) {
		t.Error("identical peers: want true")
	}
	if peersEqual(a, b) {
		t.Error("different peers: want false")
	}
	if peersEqual(a, nil) {
		t.Error("nil vs non-nil: want false")
	}
	if !peersEqual(nil, nil) {
		t.Error("both nil: want true")
	}
}

func TestExtractSharedSecret(t *testing.T) {
	noPeers := &v1alpha1.DRBDResource{}
	withPeers := &v1alpha1.DRBDResource{
		Spec: v1alpha1.DRBDResourceSpec{
			Peers: []v1alpha1.DRBDResourcePeer{{SharedSecret: "my-secret"}},
		},
	}

	if got := extractSharedSecret([]*v1alpha1.DRBDResource{noPeers}); got != "" {
		t.Errorf("no peers: got %q, want empty", got)
	}
	if got := extractSharedSecret([]*v1alpha1.DRBDResource{withPeers}); got != "my-secret" {
		t.Errorf("with peers: got %q, want %q", got, "my-secret")
	}
	if got := extractSharedSecret([]*v1alpha1.DRBDResource{noPeers, withPeers}); got != "my-secret" {
		t.Errorf("mixed: got %q, want %q", got, "my-secret")
	}
}
