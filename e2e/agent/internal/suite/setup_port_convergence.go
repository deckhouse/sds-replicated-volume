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
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupAdoptExistingPort tests port adoption: force-deletes two peered DRBDRs
// (leaving DRBD running on the nodes), then recreates them and asserts the
// agent adopts the original DRBD ports instead of allocating new ones.
func SetupAdoptExistingPort(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	prefix string,
) {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	var peerConnectedTimeout PeerConnectedTimeout
	e.Options(&testID, &drbdrConfiguredTimeout, &peerConnectedTimeout)

	// Create 2 diskful replicas, peer and sync them.
	drbdrs := make([]*v1alpha1.DRBDResource, 2)
	for i := range drbdrs {
		drbdrs[i], _ = SetupDisklessToDiskfulReplica(e, cl, cluster, prefix, i)
	}
	drbdrs = SetupPeering(e, cl, drbdrs)
	SetupInitialSync(e, cl, drbdrs)

	// Record original ports.
	originalPorts := make([]uint, 2)
	for i, drbdr := range drbdrs {
		if len(drbdr.Status.Addresses) == 0 {
			e.Fatalf("assert: DRBDResource %q has no addresses", drbdr.Name)
		}
		originalPorts[i] = drbdr.Status.Addresses[0].Address.Port
	}

	// Force-delete both DRBDRs (DRBD keeps running on nodes).
	for _, drbdr := range drbdrs {
		forceDeleteDRBDResource(e, cl, drbdr, drbdrConfiguredTimeout)
	}

	// Recreate fresh DRBDRs (empty status).
	newDRBDRs := make([]*v1alpha1.DRBDResource, 2)
	for i := range newDRBDRs {
		node := cluster.Nodes[i]
		name := testID.ResourceName(prefix, fmt.Sprintf("%d", i))
		newDRBDRs[i] = kubetesting.SetupResource(
			e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
			cl,
			newDRBDResourceDiskless(name, node.Name, uint8(i)),
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, newDRBDRs[i])
	}

	// Assert: adopted ports match original DRBD ports.
	for i, drbdr := range newDRBDRs {
		if len(drbdr.Status.Addresses) == 0 {
			e.Fatalf("assert: recreated DRBDResource %q has no addresses", drbdr.Name)
		}
		got := drbdr.Status.Addresses[0].Address.Port
		if got != originalPorts[i] {
			e.Errorf("assert: DRBDResource %q port = %d, want %d (adopted from DRBD)",
				drbdr.Name, got, originalPorts[i])
		}
	}

	// Re-peer and verify connection.
	SetupPeering(e, cl, newDRBDRs)
}

// SetupPathMismatchConvergence tests path convergence: injects a wrong port
// into DRBD on one node and asserts the agent converges to the correct port
// from status.addresses — adding the correct path, waiting for it to become
// established, and removing the stale one.
func SetupPathMismatchConvergence(
	e envtesting.E,
	cl client.WithWatch,
	drbdrs []*v1alpha1.DRBDResource,
	ne *kubetesting.NodeExec,
	cluster *Cluster,
) {
	var peerConnectedTimeout PeerConnectedTimeout
	e.Options(&peerConnectedTimeout)

	drbdr0 := drbdrs[0]
	node0 := cluster.Nodes[0]
	drbdResName := "sdsrv-" + drbdr0.Name

	intendedPort := drbdr0.Status.Addresses[0].Address.Port

	// Get current peer info for node 0's connection.
	if len(drbdr0.Status.Peers) == 0 {
		e.Fatalf("require: DRBDResource %q has no peers", drbdr0.Name)
	}
	peer := drbdr0.Status.Peers[0]
	if len(peer.Paths) == 0 {
		e.Fatalf("require: DRBDResource %q peer %q has no paths", drbdr0.Name, peer.Name)
	}
	currentLocalAddr := fmt.Sprintf("%s:%d", peer.Paths[0].Address.IPv4, peer.Paths[0].Address.Port)

	// Build remote addr from peer's spec.
	var remoteAddr string
	for _, specPeer := range drbdr0.Spec.Peers {
		if specPeer.Name == peer.Name && len(specPeer.Paths) > 0 {
			remoteAddr = fmt.Sprintf("%s:%d", specPeer.Paths[0].Address.IPv4, specPeer.Paths[0].Address.Port)
			break
		}
	}
	if remoteAddr == "" {
		e.Fatalf("require: could not find remote address for peer %q", peer.Name)
	}

	wrongPort := uint(7999)
	wrongLocalAddr := fmt.Sprintf("%s:%d", drbdr0.Status.Addresses[0].Address.IPv4, wrongPort)

	// Inject wrong path: disconnect, del-path, new-path with wrong port, connect.
	ne.Exec(e, node0.Name, "drbdsetup", "disconnect", drbdResName,
		fmt.Sprintf("%d", peer.NodeID))
	ne.Exec(e, node0.Name, "drbdsetup", "del-path", drbdResName,
		fmt.Sprintf("%d", peer.NodeID), currentLocalAddr, remoteAddr)
	ne.Exec(e, node0.Name, "drbdsetup", "new-path", drbdResName,
		fmt.Sprintf("%d", peer.NodeID), wrongLocalAddr, remoteAddr)
	ne.Exec(e, node0.Name, "drbdsetup", "connect", drbdResName,
		fmt.Sprintf("%d", peer.NodeID))

	// Wait for the agent to converge: correct path established.
	watcherScope := e.Scope()
	defer watcherScope.Close()
	waitFn := kubetesting.SetupResourceWatcher(watcherScope, cl, drbdr0)

	connectedScope := e.ScopeWithTimeout(peerConnectedTimeout.Duration)
	defer connectedScope.Close()

	drbdr0 = waitFn(connectedScope, drbdr0, func(d *v1alpha1.DRBDResource) bool {
		for _, p := range d.Status.Peers {
			if p.Name == peer.Name {
				if p.ConnectionState != v1alpha1.ConnectionStateConnected {
					return false
				}
				for _, path := range p.Paths {
					if path.Address.Port == intendedPort && path.Established {
						return true
					}
				}
			}
		}
		return false
	})

	// Assert: peer path uses the intended port.
	for _, p := range drbdr0.Status.Peers {
		if p.Name != peer.Name {
			continue
		}
		if len(p.Paths) == 0 {
			e.Fatalf("assert: peer %q has no paths after convergence", peer.Name)
		}
		if p.Paths[0].Address.Port != intendedPort {
			e.Errorf("assert: peer %q path port = %d, want %d",
				peer.Name, p.Paths[0].Address.Port, intendedPort)
		}
	}

	// Assert: DRBD on node 0 has exactly one path (stale path removed).
	statusJSON := ne.Exec(e, node0.Name, "drbdsetup", "status", drbdResName, "--json")
	assertSinglePathPerConnection(e, statusJSON, drbdResName)
}

func assertSinglePathPerConnection(e envtesting.E, statusJSON string, drbdResName string) {
	type pathEntry struct {
		ThisHost struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"this_host"`
	}
	type connEntry struct {
		Name  string      `json:"name"`
		Paths []pathEntry `json:"paths"`
	}
	type resEntry struct {
		Name        string      `json:"name"`
		Connections []connEntry `json:"connections"`
	}
	var result []resEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusJSON)), &result); err != nil {
		e.Fatalf("assert: parsing drbdsetup status JSON: %v", err)
	}
	for _, res := range result {
		if res.Name != drbdResName {
			continue
		}
		for _, conn := range res.Connections {
			if len(conn.Paths) != 1 {
				e.Errorf("assert: DRBD resource %q connection %q has %d paths, want 1",
					drbdResName, conn.Name, len(conn.Paths))
			}
		}
	}
}
