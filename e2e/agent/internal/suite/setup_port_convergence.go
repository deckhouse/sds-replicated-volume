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

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

func newDRBDResourceDiskful(name, nodeName string, nodeID uint8, llvSize resource.Quantity) *v1alpha1.DRBDResource {
	usable := drbdUsableSize(llvSize)
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName:             nodeName,
			State:                v1alpha1.DRBDResourceStateUp,
			SystemNetworks:       []string{"Internal"},
			NodeID:               nodeID,
			Role:                 v1alpha1.DRBDRoleSecondary,
			Type:                 v1alpha1.DRBDResourceTypeDiskful,
			LVMLogicalVolumeName: name,
			Size:                 &usable,
		},
	}
}

// SetupAdoptExistingPort tests port adoption during migration: renames DRBD
// resources on the nodes (simulating old control plane), deletes the DRBDRs,
// then creates new DRBDRs with ActualNameOnTheNode. The agent renames the DRBD
// resources back and adopts their existing ports instead of allocating new ones.
func SetupAdoptExistingPort(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	ne *kubetesting.NodeExec,
	prefix string,
) {
	var testID TestID
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&testID, &drbdrConfiguredTimeout)

	drbdrs := make([]*v1alpha1.DRBDResource, 2)
	for i := range drbdrs {
		drbdrs[i], _ = SetupDisklessToDiskfulReplica(e, cl, cluster, prefix, i)
	}
	drbdrs = SetupPeering(e, cl, drbdrs)
	SetupInitialSync(e, cl, drbdrs)

	originalPorts := make([]uint, 2)
	for i, drbdr := range drbdrs {
		if len(drbdr.Status.Addresses) == 0 {
			e.Fatalf("assert: DRBDResource %q has no addresses", drbdr.Name)
		}
		originalPorts[i] = drbdr.Status.Addresses[0].Address.Port
	}

	for i := range drbdrs {
		drbdrs[i] = SetupMaintenanceMode(e, cl, drbdrs[i])
	}

	migratedNames := make([]string, 2)
	for i, drbdr := range drbdrs {
		standardName := "sdsrv-" + drbdr.Name
		migratedNames[i] = "migrated-" + drbdr.Name
		ne.Exec(e, cluster.Nodes[i].Name, "drbdsetup", "rename-resource",
			standardName, migratedNames[i])
	}

	for _, drbdr := range drbdrs {
		forceDeleteDRBDResource(e, cl, drbdr, drbdrConfiguredTimeout)
	}

	newDRBDRs := make([]*v1alpha1.DRBDResource, 2)
	for i := range newDRBDRs {
		node := cluster.Nodes[i]
		name := testID.ResourceName(prefix, fmt.Sprintf("%d", i))
		obj := newDRBDResourceDiskful(name, node.Name, uint8(i), cluster.AllocateSize)
		obj.Spec.ActualNameOnTheNode = migratedNames[i]
		newDRBDRs[i] = kubetesting.SetupResource(
			e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
			cl,
			obj,
			isDRBDRTerminal,
		)
		assertDRBDRConfigured(e, newDRBDRs[i])
	}

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
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	e.Options(&peerConnectedTimeout, &drbdrConfiguredTimeout)

	drbdr0 := &v1alpha1.DRBDResource{}
	if err := cl.Get(e.Context(), client.ObjectKeyFromObject(drbdrs[0]), drbdr0); err != nil {
		e.Fatalf("require: getting DRBDResource %q: %v", drbdrs[0].Name, err)
	}

	node0 := cluster.Nodes[0]
	drbdResName := "sdsrv-" + drbdr0.Name

	intendedPort := drbdr0.Status.Addresses[0].Address.Port

	if len(drbdr0.Status.Peers) == 0 {
		e.Fatalf("require: DRBDResource %q has no peers", drbdr0.Name)
	}
	peer := drbdr0.Status.Peers[0]
	if len(peer.Paths) == 0 {
		e.Fatalf("require: DRBDResource %q peer %q has no paths", drbdr0.Name, peer.Name)
	}
	currentLocalAddr := fmt.Sprintf("%s:%d", peer.Paths[0].Address.IPv4, peer.Paths[0].Address.Port)

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

	drbdr0 = SetupMaintenanceMode(e, cl, drbdr0)

	wrongPort := uint(7999)
	wrongLocalAddr := fmt.Sprintf("%s:%d", drbdr0.Status.Addresses[0].Address.IPv4, wrongPort)

	ne.Exec(e, node0.Name, "drbdsetup", "disconnect", drbdResName,
		fmt.Sprintf("%d", peer.NodeID))
	ne.Exec(e, node0.Name, "drbdsetup", "del-path", drbdResName,
		fmt.Sprintf("%d", peer.NodeID), currentLocalAddr, remoteAddr)
	ne.Exec(e, node0.Name, "drbdsetup", "new-path", drbdResName,
		fmt.Sprintf("%d", peer.NodeID), wrongLocalAddr, remoteAddr)

	kubetesting.SetupResourcePatch(e, cl, client.ObjectKeyFromObject(drbdr0),
		func(d *v1alpha1.DRBDResource) {
			d.Spec.Maintenance = ""
		}, nil)

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
