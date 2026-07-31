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
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// drbdResourceNamePrefix mirrors the agent's drbdNamePrefix: every DRBD
// resource the agent creates for a DRBDResource object is named
// "sdsrv-<k8s name>". The suite cannot import the agent's internal package, so
// the constant lives here and is pinned by a unit test.
const drbdResourceNamePrefix = "sdsrv-"

// drbdConnectionStateConnected is the only connection state that proves the
// peer is actually participating in the resource.
const drbdConnectionStateConnected = "Connected"

// drbdDiskStateDiskless is the disk state `drbdsetup status` reports for a
// device that carries no backing disk.
const drbdDiskStateDiskless = "Diskless"

const (
	drbdPeerSettleTimeout = 5 * time.Minute
	drbdPeerSettlePoll    = 5 * time.Second

	drbdDisklessSettleTimeout = 5 * time.Minute
	drbdDisklessSettlePoll    = 5 * time.Second
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
//
// Name, PeerNodeID and ConnectionState describe the connection itself; the
// replication fields come from its single peer device (`peer_devices[0]`),
// which exists because a resource of this module carries exactly one device.
type DRBDConnection struct {
	Name            string // peer-<replica id>
	PeerNodeID      int
	ConnectionState string

	// ReplicationState is `peer_devices[].replication-state`: "Established"
	// once the two sides are in sync and replicating, "SyncSource"/"SyncTarget"
	// while a resync runs, "Off" while the connection is down.
	ReplicationState string

	// OutOfSyncKiB is `peer_devices[].out-of-sync`: the amount of data known to
	// differ between the two sides, in KiB. It is drbdOutOfSyncUnknown when the
	// node reported no peer device for this connection at all — an absent
	// counter must never be read as "nothing is out of sync", which is exactly
	// what a plain zero would say.
	OutOfSyncKiB int
}

// drbdOutOfSyncUnknown marks an out-of-sync counter the node did not report.
const drbdOutOfSyncUnknown = -1

// Connected reports whether the connection is established right now.
func (c DRBDConnection) Connected() bool {
	return c.ConnectionState == drbdConnectionStateConnected
}

// InSync reports that the peer device carries no out-of-sync data. It is false
// when the counter was not reported at all, so a claim about convergence can
// never pass on a missing measurement.
func (c DRBDConnection) InSync() bool {
	return c.OutOfSyncKiB == 0
}

// String renders one connection for failure messages.
func (c DRBDConnection) String() string {
	oos := "unknown"
	if c.OutOfSyncKiB != drbdOutOfSyncUnknown {
		oos = fmt.Sprintf("%dKiB", c.OutOfSyncKiB)
	}
	return fmt.Sprintf("%s=%s repl=%s out-of-sync=%s", c.Name, c.ConnectionState, c.ReplicationState, oos)
}

// DRBDStatus is the runtime state of one resource on one node, parsed from
// `drbdsetup status --json`.
//
// Suspended and SuspendedQuorum come from the RESOURCE level of the dump, the
// rest of the fields from its single device: `drbdsetup` prints the I/O freeze
// per resource (`suspended`, `suspended-user`, `suspended-no-data`,
// `suspended-fencing`, `suspended-quorum`) and the disk per device.
type DRBDStatus struct {
	Name  string
	Role  string
	Minor int
	// DiskState is `devices[].disk-state`: "UpToDate", "Diskless",
	// "Inconsistent", …
	DiskState string
	// IntentionalDiskless is `devices[].client`, which drbdsetup prints from
	// the kernel's device_conf.intentional_diskless. It tells a replica that is
	// diskless BY DESIGN (a tie-breaker, an access replica, a diskless client)
	// apart from one that merely lost its disk — the distinction the
	// D8DrbdDeviceIsUnintentionalDiskless alert is built on.
	IntentionalDiskless bool
	Quorum              bool // the device has quorum
	// Suspended is the resource-level `suspended`: I/O is frozen for any
	// reason (user, no-data, fencing, quorum).
	Suspended bool
	// SuspendedQuorum narrows Suspended down to the quorum cause, which is
	// what `on-no-quorum: suspend-io` produces.
	SuspendedQuorum bool
	Connections     []DRBDConnection
}

// Diskless reports whether the device currently carries no backing disk. It
// says nothing about whether that is intentional — see IntentionalDiskless.
func (s DRBDStatus) Diskless() bool {
	return s.DiskState == drbdDiskStateDiskless
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
	parts = append(parts, fmt.Sprintf(
		"%s role=%s minor=%d disk=%s client=%t quorum=%t suspended=%t suspended-quorum=%t",
		s.Name, s.Role, s.Minor, s.DiskState, s.IntentionalDiskless, s.Quorum,
		s.Suspended, s.SuspendedQuorum))
	for i := range s.Connections {
		parts = append(parts, s.Connections[i].String())
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

// AwaitIntentionalDiskless blocks until the device of resourceName on nodeName
// is diskless BY DESIGN — `drbdsetup status` reporting both disk-state
// "Diskless" and client:yes — and fails the spec when it does not get there.
//
// The two halves are separate facts and both have to be asserted. "Diskless"
// is the state of the disk; client:yes is the kernel's record of WHY it is
// diskless (device_conf.intentional_diskless), written once when the minor is
// created (`new-minor --diskless`) or when the disk is dropped on purpose
// (`detach --diskless`). A replica converted to a tie-breaker with a plain
// `detach` ends up diskless with client:no — indistinguishable, to the kernel
// and to monitoring, from a replica whose disk failed.
//
// Waiting is for the first half only: the flag is written together with the
// state transition, so once the device reports Diskless the flag is final and
// this helper fails right away instead of polling out its whole budget.
func (f *Framework) AwaitIntentionalDiskless(ctx context.Context, nodeName, resourceName string) {
	GinkgoHelper()
	err := f.awaitIntentionalDiskless(ctx, nodeName, resourceName,
		drbdDisklessSettleTimeout, drbdDisklessSettlePoll)
	if err != nil {
		Fail(err.Error())
	}
}

// AwaitIntentionalDiskless asserts that this volume has exactly wantDiskless
// replicas of a diskless type (TieBreaker, Access) and that every one of them
// came up on its node as an intentional diskless client (see
// Framework.AwaitIntentionalDiskless).
//
// wantDiskless is not a convenience — it is what keeps the assertion from
// passing on a volume that has nothing to check. "Every diskless replica is
// fine" is satisfied for free by a volume whose tie-breaker never appeared, so
// the count the spec has already proved on the API side is restated here and
// the node-side claim is made about a known number of replicas.
//
// The replica type is read from rvr.spec.type rather than from the datamesh
// member type on purpose: the spec type is the intent (and flips in place on a
// retype), while the datamesh publishes transitional types such as
// LiminalDiskful for a replica that is only passing through the diskless stage
// on its way to becoming diskful.
func (t *TestRV) AwaitIntentionalDiskless(ctx context.Context, wantDiskless int) {
	GinkgoHelper()

	type disklessReplica struct{ name, node, resource string }
	var replicas []disklessReplica
	var described []string

	for _, r := range t.TestRVRs() {
		if !r.IsPresent() {
			continue
		}
		switch r.Object().Spec.Type {
		case v1alpha1.ReplicaTypeTieBreaker, v1alpha1.ReplicaTypeAccess:
			rep := disklessReplica{
				name:     r.Name(),
				node:     r.Object().Spec.NodeName,
				resource: r.DRBDResourceName(),
			}
			replicas = append(replicas, rep)
			described = append(described,
				fmt.Sprintf("%s (%s) on node %q", rep.name, r.Object().Spec.Type, rep.node))
		}
	}

	Expect(replicas).To(HaveLen(wantDiskless),
		"volume %s has %d diskless replicas (TieBreaker/Access), expected %d: [%s]",
		t.Name(), len(replicas), wantDiskless, strings.Join(described, ", "))

	for _, r := range replicas {
		Expect(r.node).NotTo(BeEmpty(),
			"diskless replica %s is not scheduled on any node, so it has no device to check", r.name)
		t.f.AwaitIntentionalDiskless(ctx, r.node, r.resource)
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

// awaitIntentionalDiskless polls the node until its device for resourceName is
// diskless and flagged as an intentional diskless client.
//
// Unlike awaitDRBDPeers it does not give up on the first unreadable answer: a
// replica is a datamesh member in the API before the agent has created its
// minor, so "no such resource on this node" is a state that passes rather than
// a verdict. The last problem seen is carried into the timeout message so the
// failure still says what the node was answering.
func (f *Framework) awaitIntentionalDiskless(
	ctx context.Context,
	nodeName, resourceName string,
	timeout, poll time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	var lastProblem error

	for {
		st, err := f.drbdStatus(ctx, nodeName, resourceName)
		switch {
		case err != nil:
			lastProblem = err
		case st.Diskless() && st.IntentionalDiskless:
			return nil
		case st.Diskless():
			// Terminal, so there is nothing to wait for: the kernel writes
			// intentional_diskless when the minor is created or detached and
			// never revises it for a live device.
			return fmt.Errorf("drbd resource %q on node %q is diskless but NOT intentionally diskless:"+
				" `drbdsetup status` reports client:no, which is the kernel saying this replica lost its"+
				" disk rather than gave it up on purpose. It is exactly the state the"+
				" D8DrbdDeviceIsUnintentionalDiskless alert fires on, and it is what a plain"+
				" `drbdsetup detach` leaves behind where `detach --diskless` was meant. Node state: %s",
				resourceName, nodeName, st)
		default:
			lastProblem = fmt.Errorf("device is not diskless yet: %s", st)
		}

		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %s waiting for drbd resource %q on node %q to come up as an"+
				" intentional diskless client (disk-state %q and client:yes); last problem: %v",
				timeout, resourceName, nodeName, drbdDiskStateDiskless, lastProblem)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for drbd resource %q on node %q to become an intentional diskless"+
				" client: %w; last problem: %v", resourceName, nodeName, ctx.Err(), lastProblem)
		case <-time.After(poll):
		}
	}
}

// ---------------------------------------------------------------------------
// Parsers
// ---------------------------------------------------------------------------

// drbdStatusJSON is the subset of `drbdsetup status --json` the suite depends on.
//
// The freeze flags sit next to name/role because that is where drbdsetup prints
// them — they describe the resource, not the device (drbd-utils,
// user/v9/drbdsetup.c, resource_status_json).
type drbdStatusJSON struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	Suspended       bool   `json:"suspended"`
	SuspendedQuorum bool   `json:"suspended-quorum"`
	Devices         []struct {
		Minor     int    `json:"minor"`
		DiskState string `json:"disk-state"`
		// Client is the kernel's intentional-diskless flag. drbdsetup prints
		// the tri-state "unknown" as false, so an absent field and an
		// unintentionally diskless device look the same here — which is the
		// safe direction: it can only make an assertion stricter.
		Client bool `json:"client"`
		Quorum bool `json:"quorum"`
	} `json:"devices"`
	Connections []struct {
		Name            string `json:"name"`
		PeerNodeID      int    `json:"peer-node-id"`
		ConnectionState string `json:"connection-state"`
		// PeerDevices is the per-volume half of a connection. drbdsetup spells
		// this key with an underscore while every field inside it is hyphenated
		// (drbd-utils, user/v9/drbdsetup.c); the agent's own parser
		// (images/agent/pkg/drbdutils/status.go) reads the very same shape.
		PeerDevices []struct {
			ReplicationState string `json:"replication-state"`
			OutOfSync        *int   `json:"out-of-sync"`
		} `json:"peer_devices"`
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
			Name:                r.Name,
			Role:                r.Role,
			Minor:               r.Devices[0].Minor,
			DiskState:           r.Devices[0].DiskState,
			IntentionalDiskless: r.Devices[0].Client,
			Quorum:              r.Devices[0].Quorum,
			Suspended:           r.Suspended,
			SuspendedQuorum:     r.SuspendedQuorum,
		}
		for j := range r.Connections {
			c := &r.Connections[j]
			conn := DRBDConnection{
				Name:            c.Name,
				PeerNodeID:      c.PeerNodeID,
				ConnectionState: c.ConnectionState,
				OutOfSyncKiB:    drbdOutOfSyncUnknown,
			}
			// The resource carries exactly one device (checked above), so a
			// connection carries exactly one peer device — unless the node
			// reports none, which happens while a connection is being set up
			// and must stay distinguishable from "reported zero".
			if len(c.PeerDevices) > 1 {
				return DRBDStatus{}, fmt.Errorf(
					"drbd resource %q reports %d peer devices on connection %q, expected at most 1",
					resourceName, len(c.PeerDevices), c.Name)
			}
			if len(c.PeerDevices) == 1 {
				pd := &c.PeerDevices[0]
				conn.ReplicationState = pd.ReplicationState
				if pd.OutOfSync != nil {
					conn.OutOfSyncKiB = *pd.OutOfSync
				}
			}
			st.Connections = append(st.Connections, conn)
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
