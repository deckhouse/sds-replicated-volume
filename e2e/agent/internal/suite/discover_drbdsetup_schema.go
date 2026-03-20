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
	"strings"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// DiscoverDRBDSetupSchema validates that the JSON output of drbdsetup status
// and drbdsetup show strictly matches the Go structs in the drbdutils package.
//
// Uses json.Decoder with DisallowUnknownFields to detect JSON fields not
// modeled in the Go structs. Additionally validates that key fields are
// populated: resource name, connections, paths with non-empty host addresses.
//
// Requires: a fully peered DRBD resource with at least one connection and path.
func DiscoverDRBDSetupSchema(
	e envtesting.E,
	ne *kubetesting.NodeExec,
	nodeName string,
	drbdResName string,
	minConnections int,
) {
	if drbdResName == "" {
		e.Fatal("require: drbdResName must not be empty")
	}
	if minConnections < 1 {
		e.Fatal("require: minConnections must be >= 1")
	}

	discoverStatusSchema(e, ne, nodeName, drbdResName, minConnections)
	discoverShowSchema(e, ne, nodeName, drbdResName, minConnections)
}

func discoverStatusSchema(
	e envtesting.E,
	ne *kubetesting.NodeExec,
	nodeName, drbdResName string,
	minConnections int,
) {
	statusJSON := ne.Exec(e, nodeName, "drbdsetup", "status", "--json", drbdResName)
	e.Logf("drbdsetup status --json %s:\n%s", drbdResName, statusJSON)

	var result drbdutils.StatusResult
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(statusJSON)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		e.Fatalf("assert: strict unmarshal of drbdsetup status --json failed: %v", err)
	}

	if len(result) != 1 {
		e.Fatalf("assert: expected 1 resource in status, got %d", len(result))
	}
	res := &result[0]

	if res.Name != drbdResName {
		e.Fatalf("assert: status resource name = %q, want %q", res.Name, drbdResName)
	}
	if res.Role == "" {
		e.Errorf("assert: status role is empty")
	}
	if len(res.Devices) == 0 {
		e.Errorf("assert: status has no devices")
	}
	if len(res.Connections) < minConnections {
		e.Fatalf("assert: status has %d connections, want >= %d", len(res.Connections), minConnections)
	}

	for i := range res.Connections {
		conn := &res.Connections[i]
		if conn.Name == "" {
			e.Errorf("assert: status connection[%d] name is empty", i)
		}
		if conn.ConnectionState == "" {
			e.Errorf("assert: status connection[%d] state is empty", i)
		}
		if len(conn.Paths) == 0 {
			e.Fatalf("assert: status connection[%d] (%s) has no paths", i, conn.Name)
		}
		for j := range conn.Paths {
			assertStatusPath(e, &conn.Paths[j], i, j)
		}
		if len(conn.PeerDevices) == 0 {
			e.Errorf("assert: status connection[%d] (%s) has no peer_devices", i, conn.Name)
		}
	}
}

func assertStatusPath(e envtesting.E, p *drbdutils.Path, connIdx, pathIdx int) {
	if p.ThisHost.Address == "" {
		e.Errorf("assert: status conn[%d].paths[%d].this_host.address is empty", connIdx, pathIdx)
	}
	if p.ThisHost.Port == 0 {
		e.Errorf("assert: status conn[%d].paths[%d].this_host.port is 0", connIdx, pathIdx)
	}
	if p.ThisHost.Family == "" {
		e.Errorf("assert: status conn[%d].paths[%d].this_host.family is empty", connIdx, pathIdx)
	}
	if p.RemoteHost.Address == "" {
		e.Errorf("assert: status conn[%d].paths[%d].remote_host.address is empty", connIdx, pathIdx)
	}
	if p.RemoteHost.Port == 0 {
		e.Errorf("assert: status conn[%d].paths[%d].remote_host.port is 0", connIdx, pathIdx)
	}
	if p.RemoteHost.Family == "" {
		e.Errorf("assert: status conn[%d].paths[%d].remote_host.family is empty", connIdx, pathIdx)
	}
}

func discoverShowSchema(
	e envtesting.E,
	ne *kubetesting.NodeExec,
	nodeName, drbdResName string,
	minConnections int,
) {
	showJSON := ne.Exec(e, nodeName, "drbdsetup", "show", "--json", "--show-defaults", drbdResName)
	e.Logf("drbdsetup show --json --show-defaults %s:\n%s", drbdResName, showJSON)

	var result []drbdutils.ShowResource
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(showJSON)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		e.Fatalf("assert: strict unmarshal of drbdsetup show --json failed: %v", err)
	}

	if len(result) != 1 {
		e.Fatalf("assert: expected 1 resource in show, got %d", len(result))
	}
	res := &result[0]

	if res.Resource != drbdResName {
		e.Fatalf("assert: show resource name = %q, want %q", res.Resource, drbdResName)
	}
	if len(res.ThisHost.Volumes) == 0 {
		e.Errorf("assert: show _this_host has no volumes")
	}
	if len(res.Connections) < minConnections {
		e.Fatalf("assert: show has %d connections, want >= %d", len(res.Connections), minConnections)
	}

	for i := range res.Connections {
		conn := &res.Connections[i]
		if conn.Net.Protocol == "" {
			e.Errorf("assert: show connection[%d] net.protocol is empty", i)
		}
		if len(conn.Paths) == 0 {
			e.Fatalf("assert: show connection[%d] has no paths", i)
		}
		for j := range conn.Paths {
			assertShowPath(e, &conn.Paths[j], i, j)
		}
	}
}

func assertShowPath(e envtesting.E, p *drbdutils.ShowPath, connIdx, pathIdx int) {
	if p.ThisHost.Address == "" {
		e.Errorf("assert: show conn[%d].paths[%d].this_host.address is empty", connIdx, pathIdx)
	}
	if p.ThisHost.Port == 0 {
		e.Errorf("assert: show conn[%d].paths[%d].this_host.port is 0", connIdx, pathIdx)
	}
	if p.ThisHost.Family == "" {
		e.Errorf("assert: show conn[%d].paths[%d].this_host.family is empty", connIdx, pathIdx)
	}
	if p.RemoteHost.Address == "" {
		e.Errorf("assert: show conn[%d].paths[%d].remote_host.address is empty", connIdx, pathIdx)
	}
	if p.RemoteHost.Port == 0 {
		e.Errorf("assert: show conn[%d].paths[%d].remote_host.port is 0", connIdx, pathIdx)
	}
	if p.RemoteHost.Family == "" {
		e.Errorf("assert: show conn[%d].paths[%d].remote_host.family is empty", connIdx, pathIdx)
	}
}
