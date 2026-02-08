/*
Copyright 2025 Flant JSC

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

package drbdrlop

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// computePeersForConfigurePeers builds the desired spec.peers for localDRBDR from other resources in allDRBDRs (status.addresses). Pure, no I/O.
func computePeersForConfigurePeers(localDRBDR *v1alpha1.DRBDResource, allDRBDRs []*v1alpha1.DRBDResource) ([]v1alpha1.DRBDResourcePeer, error) {
	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(allDRBDRs)-1)
	for _, p := range allDRBDRs {
		if p.Name == localDRBDR.Name {
			continue
		}
		peer, err := peerFromDRBDResource(p)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// applyPeersToDRBDR sets drbdr.Spec.Peers to peers. Mutates drbdr in place.
func applyPeersToDRBDR(drbdr *v1alpha1.DRBDResource, peers []v1alpha1.DRBDResourcePeer) {
	drbdr.Spec.Peers = peers
}

// peerFromDRBDResource builds a DRBDResourcePeer from a DRBDResource using its spec (name, nodeID, type) and status.addresses for paths. Pure, no I/O.
func peerFromDRBDResource(drbdr *v1alpha1.DRBDResource) (v1alpha1.DRBDResourcePeer, error) {
	if len(drbdr.Status.Addresses) == 0 {
		return v1alpha1.DRBDResourcePeer{}, fmt.Errorf("DRBDResource %q has no status.addresses", drbdr.Name)
	}
	paths := make([]v1alpha1.DRBDResourcePath, 0, len(drbdr.Status.Addresses))
	for i := range drbdr.Status.Addresses {
		addr := &drbdr.Status.Addresses[i]
		paths = append(paths, v1alpha1.DRBDResourcePath{
			SystemNetworkName: addr.SystemNetworkName,
			Address:           addr.Address,
		})
	}
	return v1alpha1.DRBDResourcePeer{
		Name:            drbdr.Spec.NodeName,
		Type:            drbdr.Spec.Type,
		AllowRemoteRead: true,
		NodeID:          drbdr.Spec.NodeID,
		Protocol:        v1alpha1.DRBDProtocolC,
		Paths:           paths,
	}, nil
}
