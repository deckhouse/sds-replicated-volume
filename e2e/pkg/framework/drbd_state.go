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

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

// drbdResourceNamePrefix mirrors the agent's drbdNamePrefix: every DRBD
// resource the agent creates for a DRBDResource object is named
// "sdsrv-<k8s name>". The suite cannot import the agent's internal package, so
// the constant lives here and is pinned by a unit test.
const drbdResourceNamePrefix = "sdsrv-"

// drbdConnectionStateConnected is the only connection state that proves the
// peer is actually participating in the resource.
const drbdConnectionStateConnected = "Connected"

const (
	drbdPeerSettleTimeout = 5 * time.Minute
	drbdPeerSettlePoll    = 5 * time.Second
)

// DRBDResourceName returns the resource name the kernel of a node knows for the
// DRBDResource (and therefore for the ReplicatedVolumeReplica) called k8sName.
//
// It is only valid for resources the suite itself created: an adopted resource
// carries its pre-existing name in DRBDResource.spec.actualNameOnTheNode.
func DRBDResourceName(k8sName string) string {
	return drbdResourceNamePrefix + k8sName
}

// DRBDResourceName returns the kernel-side DRBD resource name of this replica.
//
// An adopted resource keeps the name it had before the module took it over,
// which the agent records in DRBDResource.spec.actualNameOnTheNode; that name
// wins over the derived one whenever the DRBDResource is already observed.
func (t *TestRVR) DRBDResourceName() string {
	if drbdr := t.DRBDR(); drbdr.IsPresent() {
		if actual := drbdr.Object().Spec.ActualNameOnTheNode; actual != "" {
			return actual
		}
	}
	return DRBDResourceName(t.Name())
}

// DRBDPeerName returns the connection name the agent gives to the peer with the
// given replica id (`drbdsetup new-peer --_name peer-<id>`). It is the identity
// under which a replica shows up in its peers' DRBD configuration, and it is
// what makes a departed peer observable on the node rather than only in status.
func DRBDPeerName(replicaID uint8) string {
	return fmt.Sprintf("peer-%d", replicaID)
}

// DRBDConnection is one peer connection of a resource, as `drbdsetup status`
// reports it on the node.
type DRBDConnection struct {
	Name            string // peer-<replica id>
	PeerNodeID      int
	ConnectionState string
}

// Connected reports whether the connection is established right now.
func (c DRBDConnection) Connected() bool {
	return c.ConnectionState == drbdConnectionStateConnected
}

// DRBDStatus is the runtime state of one resource on one node, parsed from
// `drbdsetup status --json`.
type DRBDStatus struct {
	Name        string
	Role        string
	Minor       int
	Quorum      bool // the device has quorum
	Connections []DRBDConnection
}

// PeerNames returns the sorted names of all configured connections.
func (s DRBDStatus) PeerNames() []string {
	names := make([]string, 0, len(s.Connections))
	for i := range s.Connections {
		names = append(names, s.Connections[i].Name)
	}
	slices.Sort(names)
	return names
}

// ConnectedPeerNames returns the sorted names of the connections that are
// currently established.
func (s DRBDStatus) ConnectedPeerNames() []string {
	var names []string
	for i := range s.Connections {
		if s.Connections[i].Connected() {
			names = append(names, s.Connections[i].Name)
		}
	}
	slices.Sort(names)
	return names
}

// Connection returns the connection with the given peer name.
func (s DRBDStatus) Connection(peerName string) (DRBDConnection, bool) {
	for i := range s.Connections {
		if s.Connections[i].Name == peerName {
			return s.Connections[i], true
		}
	}
	return DRBDConnection{}, false
}

// String renders the status for failure messages.
func (s DRBDStatus) String() string {
	parts := make([]string, 0, len(s.Connections)+1)
	parts = append(parts, fmt.Sprintf("%s role=%s minor=%d quorum=%t", s.Name, s.Role, s.Minor, s.Quorum))
	for i := range s.Connections {
		parts = append(parts, fmt.Sprintf("%s=%s", s.Connections[i].Name, s.Connections[i].ConnectionState))
	}
	return strings.Join(parts, " ")
}

// DRBDConfigPeer is one peer of the on-node resource configuration.
type DRBDConfigPeer struct {
	Name       string // peer-<replica id>
	PeerNodeID int
}

// DRBDConfig is the CONFIGURATION of one resource on one node, parsed from
// `drbdsetup show --json`: which peers the kernel is configured with and which
// quorum threshold it enforces. Unlike DRBDStatus it does not depend on a peer
// being reachable, which is what makes it the ground truth for "this peer is
// (still) part of the resource".
type DRBDConfig struct {
	Name string
	// Quorum is the `quorum` resource option: the numeric voter threshold, or
	// "off"/"majority" when it is not a plain number.
	Quorum string
	Peers  []DRBDConfigPeer
}

// PeerNames returns the sorted names of all configured peers.
func (c DRBDConfig) PeerNames() []string {
	names := make([]string, 0, len(c.Peers))
	for i := range c.Peers {
		names = append(names, c.Peers[i].Name)
	}
	slices.Sort(names)
	return names
}

// HasPeer reports whether the resource is configured with the given peer.
func (c DRBDConfig) HasPeer(peerName string) bool {
	return slices.Contains(c.PeerNames(), peerName)
}

// String renders the configuration for failure messages.
func (c DRBDConfig) String() string {
	return fmt.Sprintf("%s quorum=%s peers=[%s]", c.Name, c.Quorum, strings.Join(c.PeerNames(), " "))
}

// ---------------------------------------------------------------------------
// Exported helpers
// ---------------------------------------------------------------------------

// DRBDStatus returns the runtime state of resourceName on nodeName, straight
// from the node's kernel. Use it when a claim is about connectivity or quorum:
// the CR status is the agent's report of the same facts and can lag.
func (f *Framework) DRBDStatus(ctx context.Context, nodeName, resourceName string) DRBDStatus {
	GinkgoHelper()
	st, err := f.drbdStatus(ctx, nodeName, resourceName)
	if err != nil {
		Fail(fmt.Sprintf("drbd status of %q on node %q: %v", resourceName, nodeName, err))
	}
	return st
}

// DRBDConfig returns the on-node configuration of resourceName on nodeName.
func (f *Framework) DRBDConfig(ctx context.Context, nodeName, resourceName string) DRBDConfig {
	GinkgoHelper()
	cfg, err := f.drbdConfig(ctx, nodeName, resourceName)
	if err != nil {
		Fail(fmt.Sprintf("drbd configuration of %q on node %q: %v", resourceName, nodeName, err))
	}
	return cfg
}

// AwaitDRBDPeers blocks until the DRBD configuration of resourceName on
// nodeName holds exactly wantPeers, and fails the spec when it does not settle
// there.
//
// Tearing a peer down is not instantaneous — the agent removes it only after
// the API object is gone — so a spec that asserts a departure MUST wait for it
// instead of demanding it right away. The assertion is on the exact set, so an
// unexpected leftover peer fails just as a missing one does.
func (f *Framework) AwaitDRBDPeers(ctx context.Context, nodeName, resourceName string, wantPeers ...string) {
	GinkgoHelper()
	err := f.awaitDRBDPeers(ctx, nodeName, resourceName, wantPeers, drbdPeerSettleTimeout, drbdPeerSettlePoll)
	if err != nil {
		Fail(fmt.Sprintf("drbd peers of %q on node %q: %v", resourceName, nodeName, err))
	}
}

// ---------------------------------------------------------------------------
// Core: error-returning, unit-testable with a stub runner
// ---------------------------------------------------------------------------

func (f *Framework) drbdStatus(ctx context.Context, nodeName, resourceName string) (DRBDStatus, error) {
	res, err := f.runner().DrbdsetupRun(ctx, nodeName, "status", "--json", resourceName)
	if err != nil {
		return DRBDStatus{}, fmt.Errorf("running drbdsetup status: %w", err)
	}
	if res.ExitCode != 0 {
		return DRBDStatus{}, fmt.Errorf("drbdsetup status exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return parseDRBDStatus(res.Stdout, resourceName)
}

func (f *Framework) drbdConfig(ctx context.Context, nodeName, resourceName string) (DRBDConfig, error) {
	res, err := f.runner().DrbdsetupRun(ctx, nodeName, "show", "--json", resourceName)
	if err != nil {
		return DRBDConfig{}, fmt.Errorf("running drbdsetup show: %w", err)
	}
	if res.ExitCode != 0 {
		return DRBDConfig{}, fmt.Errorf("drbdsetup show exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return parseDRBDConfig(res.Stdout, resourceName)
}

// awaitDRBDPeers polls the node until the configured peer set equals want.
func (f *Framework) awaitDRBDPeers(
	ctx context.Context,
	nodeName, resourceName string,
	want []string,
	timeout, poll time.Duration,
) error {
	expected := slices.Clone(want)
	slices.Sort(expected)

	deadline := time.Now().Add(timeout)
	var last DRBDConfig

	for {
		cfg, err := f.drbdConfig(ctx, nodeName, resourceName)
		if err != nil {
			return err
		}
		last = cfg

		if slices.Equal(cfg.PeerNames(), expected) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for peers [%s]; last configuration: %s",
				timeout, strings.Join(expected, " "), last)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for peers [%s]: %w; last configuration: %s",
				strings.Join(expected, " "), ctx.Err(), last)
		case <-time.After(poll):
		}
	}
}

// ---------------------------------------------------------------------------
// Parsers
// ---------------------------------------------------------------------------

// drbdStatusJSON is the subset of `drbdsetup status --json` the suite depends on.
type drbdStatusJSON struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Devices []struct {
		Minor  int  `json:"minor"`
		Quorum bool `json:"quorum"`
	} `json:"devices"`
	Connections []struct {
		Name            string `json:"name"`
		PeerNodeID      int    `json:"peer-node-id"`
		ConnectionState string `json:"connection-state"`
	} `json:"connections"`
}

// parseDRBDStatus extracts the state of resourceName from a status dump. The
// dump may hold several resources (the suite always asks for one, but a node
// answering with more must not be silently misread).
func parseDRBDStatus(out, resourceName string) (DRBDStatus, error) {
	var resources []drbdStatusJSON
	if err := json.Unmarshal([]byte(out), &resources); err != nil {
		return DRBDStatus{}, fmt.Errorf("parsing drbdsetup status --json output %q: %w", truncate(out, 512), err)
	}

	for i := range resources {
		r := &resources[i]
		if r.Name != resourceName {
			continue
		}
		if len(r.Devices) != 1 {
			return DRBDStatus{}, fmt.Errorf("drbd resource %q reports %d devices, expected exactly 1",
				resourceName, len(r.Devices))
		}
		st := DRBDStatus{
			Name:   r.Name,
			Role:   r.Role,
			Minor:  r.Devices[0].Minor,
			Quorum: r.Devices[0].Quorum,
		}
		for j := range r.Connections {
			c := &r.Connections[j]
			st.Connections = append(st.Connections, DRBDConnection{
				Name:            c.Name,
				PeerNodeID:      c.PeerNodeID,
				ConnectionState: c.ConnectionState,
			})
		}
		return st, nil
	}

	return DRBDStatus{}, notFoundOnNode(resourceName, statusResourceNames(resources))
}

// drbdShowJSON is the subset of `drbdsetup show --json` the suite depends on.
type drbdShowJSON struct {
	Resource string `json:"resource"`
	Options  struct {
		Quorum string `json:"quorum"`
	} `json:"options"`
	Connections []struct {
		PeerNodeID int `json:"_peer_node_id"`
		Net        struct {
			Name string `json:"_name"`
		} `json:"net"`
	} `json:"connections"`
}

// parseDRBDConfig extracts the configuration of resourceName from a show dump.
func parseDRBDConfig(out, resourceName string) (DRBDConfig, error) {
	var resources []drbdShowJSON
	if err := json.Unmarshal([]byte(out), &resources); err != nil {
		return DRBDConfig{}, fmt.Errorf("parsing drbdsetup show --json output %q: %w", truncate(out, 512), err)
	}

	for i := range resources {
		r := &resources[i]
		if r.Resource != resourceName {
			continue
		}
		cfg := DRBDConfig{Name: r.Resource, Quorum: r.Options.Quorum}
		for j := range r.Connections {
			c := &r.Connections[j]
			cfg.Peers = append(cfg.Peers, DRBDConfigPeer{
				Name:       c.Net.Name,
				PeerNodeID: c.PeerNodeID,
			})
		}
		return cfg, nil
	}

	return DRBDConfig{}, notFoundOnNode(resourceName, showResourceNames(resources))
}

func statusResourceNames(resources []drbdStatusJSON) []string {
	names := make([]string, 0, len(resources))
	for i := range resources {
		names = append(names, resources[i].Name)
	}
	return names
}

func showResourceNames(resources []drbdShowJSON) []string {
	names := make([]string, 0, len(resources))
	for i := range resources {
		names = append(names, resources[i].Resource)
	}
	return names
}

func notFoundOnNode(resourceName string, reported []string) error {
	return fmt.Errorf("drbd resource %q not found on the node (reported: [%s])",
		resourceName, strings.Join(reported, " "))
}
