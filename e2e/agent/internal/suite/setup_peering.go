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

// SetupPeering links all DRBDResources as full-mesh peers of each other.
// Returns the updated resources after peering is configured and connected.
func SetupPeering(
	e envtesting.E,
	cl client.WithWatch,
	drbdrs []*v1alpha1.DRBDResource,
) []*v1alpha1.DRBDResource {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	var peerConnectedTimeout PeerConnectedTimeout
	e.Options(&drbdrConfiguredTimeout, &peerConnectedTimeout)

	sharedSecret := "e2e-test-shared-secret"

	type drbdrWaitFn = func(envtesting.E, *v1alpha1.DRBDResource, func(*v1alpha1.DRBDResource) bool) *v1alpha1.DRBDResource

	// Build full-mesh peer lists: resource i gets peers for all j != i.
	peerLists := make([][]v1alpha1.DRBDResourcePeer, len(drbdrs))
	for i := range drbdrs {
		for j := range drbdrs {
			if i == j {
				continue
			}
			peerLists[i] = append(peerLists[i], v1alpha1.DRBDResourcePeer{
				Name:            drbdrs[j].Spec.NodeName,
				Type:            drbdrs[j].Spec.Type,
				NodeID:          drbdrs[j].Spec.NodeID,
				Protocol:        v1alpha1.DRBDProtocolC,
				SharedSecret:    sharedSecret,
				SharedSecretAlg: v1alpha1.SharedSecretAlgSHA1,
				Paths:           addressesToPaths(drbdrs[j].Status.Addresses),
			})
		}
	}

	// Start watchers for all resources before patching.
	watcherScope := e.Scope()
	defer watcherScope.Close()
	waitFns := make([]drbdrWaitFn, len(drbdrs))
	for i, drbdr := range drbdrs {
		waitFns[i] = kubetesting.SetupResourceWatcher(watcherScope, cl, drbdr)
	}

	// Patch all resources with peer info (no predicates).
	for i := range drbdrs {
		peers := peerLists[i]
		drbdrs[i] = kubetesting.SetupResourcePatch(e, cl, client.ObjectKey{Name: drbdrs[i].Name},
			func(d *v1alpha1.DRBDResource) {
				d.Spec.Peers = peers
			}, nil)
	}

	// Wait for all to reach terminal state, then assert configured with peers.
	configuredScope := e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration)
	defer configuredScope.Close()

	for i := range drbdrs {
		drbdrs[i] = waitFns[i](configuredScope, drbdrs[i], isDRBDRTerminal)
		assertDRBDRConfigured(e, drbdrs[i])
		for _, peer := range peerLists[i] {
			assertHasPeerInStatus(e, drbdrs[i], peer)
		}
	}

	// Wait for all peer connections to be established.
	connectedScope := e.ScopeWithTimeout(peerConnectedTimeout.Duration)
	defer connectedScope.Close()

	for i := range drbdrs {
		drbdrs[i] = waitFns[i](connectedScope, drbdrs[i], allPeersConnected(peerLists[i]))
	}

	return drbdrs
}

func assertHasPeerInStatus(e envtesting.E, drbdr *v1alpha1.DRBDResource, expected v1alpha1.DRBDResourcePeer) {
	e.Helper()
	for _, peer := range drbdr.Status.Peers {
		if peer.Name != expected.Name {
			continue
		}
		if peer.NodeID != uint(expected.NodeID) {
			e.Fatalf("DRBDResource %q peer %q nodeID is %d, want %d",
				drbdr.Name, expected.Name, peer.NodeID, expected.NodeID)
		}
		if peer.Type != expected.Type {
			e.Fatalf("DRBDResource %q peer %q type is %s, want %s",
				drbdr.Name, expected.Name, peer.Type, expected.Type)
		}
		return
	}
	e.Fatalf("DRBDResource %q does not have expected peer %q in status", drbdr.Name, expected.Name)
}

func allPeersConnected(peers []v1alpha1.DRBDResourcePeer) func(*v1alpha1.DRBDResource) bool {
	return func(drbdr *v1alpha1.DRBDResource) bool {
		for _, p := range peers {
			found := false
			for _, s := range drbdr.Status.Peers {
				if s.Name == p.Name {
					if s.ConnectionState != v1alpha1.ConnectionStateConnected {
						return false
					}
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
}

func addressesToPaths(addrs []v1alpha1.DRBDResourceAddressStatus) []v1alpha1.DRBDResourcePath {
	paths := make([]v1alpha1.DRBDResourcePath, 0, len(addrs))
	for _, addr := range addrs {
		paths = append(paths, v1alpha1.DRBDResourcePath(addr))
	}
	return paths
}
