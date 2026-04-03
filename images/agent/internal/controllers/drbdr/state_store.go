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
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

// DRBDStateStore maintains an in-memory representation of DRBD runtime state,
// built from events2 stream by the Scanner and read by reconcilers.
//
// Concurrency model:
//   - Scanner goroutine is the sole writer (BeginDump/EndDump/ApplyEvent).
//   - Reconciler goroutines are readers (Snapshot/ResourceExists).
//   - Two independent blocking gates protect readers:
//     1. readyCh — blocks until the first initial dump completes (one-time).
//     2. dumpMu  — blocks during any dump (initial or reconnect).
type DRBDStateStore struct {
	readyCh   chan struct{}
	readyOnce sync.Once

	dumpMu sync.RWMutex
	dataMu sync.RWMutex

	resources map[string]*resourceState
}

func NewDRBDStateStore() *DRBDStateStore {
	return &DRBDStateStore{
		readyCh:   make(chan struct{}),
		resources: make(map[string]*resourceState),
	}
}

// BeginDump prepares for a new events2 initial dump. It blocks all readers
// (via dumpMu) and clears existing data.
func (s *DRBDStateStore) BeginDump() {
	s.dumpMu.Lock()
	s.dataMu.Lock()
	s.resources = make(map[string]*resourceState)
	s.dataMu.Unlock()
}

// EndDump signals that the events2 dump is complete. It unblocks readers and,
// on the first call, marks the store as ready.
func (s *DRBDStateStore) EndDump() {
	s.readyOnce.Do(func() { close(s.readyCh) })
	s.dumpMu.Unlock()
}

// AbortDump releases the dump lock without marking the store as ready.
func (s *DRBDStateStore) AbortDump() {
	s.dumpMu.Unlock()
}

// WaitReady blocks until the store has completed its first dump, or ctx is cancelled.
func (s *DRBDStateStore) WaitReady(ctx context.Context) bool {
	select {
	case <-s.readyCh:
		return true
	case <-ctx.Done():
		return false
	}
}

// Snapshot returns a drbdutils.Resource compatible with the drbdsetup status
// JSON output for the given DRBD resource. Returns nil if the resource is not
// known to the store.
func (s *DRBDStateStore) Snapshot(resourceName string) *drbdutils.Resource {
	s.dumpMu.RLock()
	defer s.dumpMu.RUnlock()
	s.dataMu.RLock()
	defer s.dataMu.RUnlock()

	rs, ok := s.resources[resourceName]
	if !ok {
		return nil
	}
	return rs.toStatusResource(resourceName)
}

// ResourceExists returns true if the resource is known to the store.
func (s *DRBDStateStore) ResourceExists(resourceName string) bool {
	s.dumpMu.RLock()
	defer s.dumpMu.RUnlock()
	s.dataMu.RLock()
	defer s.dataMu.RUnlock()
	_, ok := s.resources[resourceName]
	return ok
}

// ApplyEvent updates the store with an events2 event. Called by the Scanner.
// Returns an error when the event cannot be applied (missing fields, unknown kind).
// The caller is expected to log the error.
func (s *DRBDStateStore) ApplyEvent(ev *drbdutils.Event) error {
	if ev.Object == drbdutils.EventObjectDumpDone {
		return nil
	}
	name, ok := ev.State["name"]
	if !ok {
		return fmt.Errorf("event %s %s has no 'name' field", ev.Kind, ev.Object)
	}

	s.dataMu.Lock()
	defer s.dataMu.Unlock()

	switch ev.Kind {
	case drbdutils.EventKindExists, drbdutils.EventKindCreate, drbdutils.EventKindChange:
		s.upsertObject(name, ev)
	case drbdutils.EventKindDestroy:
		s.destroyObject(name, ev)
	case drbdutils.EventKindRename:
		newName, ok := ev.State["new_name"]
		if !ok {
			return fmt.Errorf("rename event for %q has no 'new_name' field", name)
		}
		rs, exists := s.resources[name]
		if !exists {
			return fmt.Errorf("rename event for %q: resource not found in store", name)
		}
		delete(s.resources, name)
		s.resources[newName] = rs
	default:
		return fmt.Errorf("unhandled event kind %q for object %s %q", ev.Kind, ev.Object, name)
	}
	return nil
}

func (s *DRBDStateStore) upsertObject(name string, ev *drbdutils.Event) {
	switch ev.Object {
	case drbdutils.EventObjectResource:
		rs := s.getOrCreateResource(name)
		applyResourceFields(rs, ev.State)
	case drbdutils.EventObjectDevice:
		rs := s.getOrCreateResource(name)
		vol := parseIntField(ev.State, "volume")
		ds := rs.getOrCreateDevice(vol)
		applyDeviceFields(ds, ev.State)
	case drbdutils.EventObjectConnection:
		rs := s.getOrCreateResource(name)
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		cs := rs.getOrCreateConnection(peerNodeID)
		applyConnectionFields(cs, ev.State)
	case drbdutils.EventObjectPeerDevice:
		rs := s.getOrCreateResource(name)
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		vol := parseIntField(ev.State, "volume")
		cs := rs.getOrCreateConnection(peerNodeID)
		pds := cs.getOrCreatePeerDevice(vol)
		applyPeerDeviceFields(pds, ev.State)
	case drbdutils.EventObjectPath:
		rs := s.getOrCreateResource(name)
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		cs := rs.getOrCreateConnection(peerNodeID)
		localKey := ev.State["local"]
		if localKey == "" {
			return
		}
		ps := cs.getOrCreatePath(localKey)
		applyPathFields(ps, ev.State)
	}
}

func (s *DRBDStateStore) destroyObject(name string, ev *drbdutils.Event) {
	rs, ok := s.resources[name]
	if !ok {
		return
	}

	switch ev.Object {
	case drbdutils.EventObjectResource:
		delete(s.resources, name)
	case drbdutils.EventObjectDevice:
		vol := parseIntField(ev.State, "volume")
		delete(rs.devices, vol)
	case drbdutils.EventObjectConnection:
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		delete(rs.connections, peerNodeID)
	case drbdutils.EventObjectPeerDevice:
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		vol := parseIntField(ev.State, "volume")
		if cs, ok := rs.connections[peerNodeID]; ok {
			delete(cs.peerDevices, vol)
		}
	case drbdutils.EventObjectPath:
		peerNodeID := parseIntField(ev.State, "peer-node-id")
		localKey := ev.State["local"]
		if cs, ok := rs.connections[peerNodeID]; ok {
			delete(cs.paths, localKey)
		}
	}
}

func (s *DRBDStateStore) getOrCreateResource(name string) *resourceState {
	rs, ok := s.resources[name]
	if !ok {
		rs = &resourceState{
			devices:     make(map[int]*deviceState),
			connections: make(map[int]*connectionState),
		}
		s.resources[name] = rs
	}
	return rs
}

// Internal state types

type resourceState struct {
	role            string
	suspended       bool
	forceIOFailures bool

	devices     map[int]*deviceState
	connections map[int]*connectionState
}

func (rs *resourceState) getOrCreateDevice(vol int) *deviceState {
	ds, ok := rs.devices[vol]
	if !ok {
		ds = &deviceState{}
		rs.devices[vol] = ds
	}
	return ds
}

func (rs *resourceState) getOrCreateConnection(peerNodeID int) *connectionState {
	cs, ok := rs.connections[peerNodeID]
	if !ok {
		cs = &connectionState{
			paths:       make(map[string]*pathState),
			peerDevices: make(map[int]*peerDeviceState),
		}
		rs.connections[peerNodeID] = cs
	}
	return cs
}

type deviceState struct {
	minor     int
	diskState string
	client    bool
	open      bool
	quorum    bool
	sizeKiB   int
}

type connectionState struct {
	name            string
	connectionState string
	peerRole        string

	paths       map[string]*pathState
	peerDevices map[int]*peerDeviceState
}

func (cs *connectionState) getOrCreatePeerDevice(vol int) *peerDeviceState {
	pds, ok := cs.peerDevices[vol]
	if !ok {
		pds = &peerDeviceState{}
		cs.peerDevices[vol] = pds
	}
	return pds
}

func (cs *connectionState) getOrCreatePath(localKey string) *pathState {
	ps, ok := cs.paths[localKey]
	if !ok {
		ps = &pathState{}
		cs.paths[localKey] = ps
	}
	return ps
}

type pathState struct {
	localIP     string
	localPort   int
	remoteIP    string
	remotePort  int
	established bool
}

type peerDeviceState struct {
	replicationState string
	peerDiskState    string
	peerClient       bool
	resyncSuspended  string
}

// Field application from events2 key-value pairs

func applyResourceFields(rs *resourceState, state map[string]string) {
	if v, ok := state["role"]; ok {
		rs.role = v
	}
	if v, ok := state["suspended"]; ok {
		rs.suspended = parseBoolField(v)
	}
	if v, ok := state["force-io-failures"]; ok {
		rs.forceIOFailures = parseBoolField(v)
	}
}

func applyDeviceFields(ds *deviceState, state map[string]string) {
	if v, ok := state["minor"]; ok {
		ds.minor = parseIntStr(v)
	}
	if v, ok := state["disk"]; ok {
		ds.diskState = v
	}
	if v, ok := state["client"]; ok {
		ds.client = parseBoolField(v)
	}
	if v, ok := state["open"]; ok {
		ds.open = parseBoolField(v)
	}
	if v, ok := state["quorum"]; ok {
		ds.quorum = parseBoolField(v)
	}
	if v, ok := state["size"]; ok {
		ds.sizeKiB = parseIntStr(v)
	}
}

func applyConnectionFields(cs *connectionState, state map[string]string) {
	if v, ok := state["conn-name"]; ok {
		cs.name = v
	}
	if v, ok := state["connection"]; ok {
		cs.connectionState = v
	}
	if v, ok := state["peer-role"]; ok {
		cs.peerRole = v
	}
}

func applyPeerDeviceFields(pds *peerDeviceState, state map[string]string) {
	if v, ok := state["replication"]; ok {
		pds.replicationState = v
	}
	if v, ok := state["peer-disk"]; ok {
		pds.peerDiskState = v
	}
	if v, ok := state["peer-client"]; ok {
		pds.peerClient = parseBoolField(v)
	}
	if v, ok := state["resync-suspended"]; ok {
		pds.resyncSuspended = v
	}
}

func applyPathFields(ps *pathState, state map[string]string) {
	if v, ok := state["local"]; ok {
		ip, port, ok := parseLocalAddr(v)
		if ok {
			ps.localIP = ip
			ps.localPort = int(port)
		}
	}
	if v, ok := state["peer"]; ok {
		ip, port, ok := parseLocalAddr(v)
		if ok {
			ps.remoteIP = ip
			ps.remotePort = int(port)
		}
	}
	if v, ok := state["established"]; ok {
		ps.established = parseBoolField(v)
	}
}

// Conversion to drbdutils status types

func (rs *resourceState) toStatusResource(name string) *drbdutils.Resource {
	res := &drbdutils.Resource{
		Name:            name,
		Role:            rs.role,
		Suspended:       rs.suspended,
		ForceIOFailures: rs.forceIOFailures,
	}

	volNums := sortedKeys(rs.devices)
	for _, vol := range volNums {
		ds := rs.devices[vol]
		res.Devices = append(res.Devices, drbdutils.Device{
			Volume:    vol,
			Minor:     ds.minor,
			DiskState: ds.diskState,
			Client:    ds.client,
			Open:      ds.open,
			Quorum:    ds.quorum,
			Size:      ds.sizeKiB,
		})
	}

	peerNodeIDs := sortedKeys(rs.connections)
	for _, peerNodeID := range peerNodeIDs {
		cs := rs.connections[peerNodeID]
		conn := drbdutils.Connection{
			PeerNodeID:      peerNodeID,
			Name:            cs.name,
			ConnectionState: cs.connectionState,
			Peerrole:        cs.peerRole,
		}

		for _, ps := range cs.paths {
			conn.Paths = append(conn.Paths, drbdutils.Path{
				ThisHost: drbdutils.Host{
					Address: ps.localIP,
					Port:    ps.localPort,
					Family:  "ipv4",
				},
				RemoteHost: drbdutils.Host{
					Address: ps.remoteIP,
					Port:    ps.remotePort,
					Family:  "ipv4",
				},
				Established: ps.established,
			})
		}

		peerVolNums := sortedKeys(cs.peerDevices)
		for _, vol := range peerVolNums {
			pds := cs.peerDevices[vol]
			conn.PeerDevices = append(conn.PeerDevices, drbdutils.PeerDevice{
				Volume:           vol,
				ReplicationState: pds.replicationState,
				PeerDiskState:    pds.peerDiskState,
				PeerClient:       pds.peerClient,
				ResyncSuspended:  pds.resyncSuspended,
			})
		}

		res.Connections = append(res.Connections, conn)
	}

	return res
}

// Test helpers

// PopulateForTest fills the store from a StatusResult. For testing only.
func (s *DRBDStateStore) PopulateForTest(result drbdutils.StatusResult) {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()

	for i := range result {
		res := &result[i]
		rs := s.getOrCreateResource(res.Name)
		rs.role = res.Role
		rs.suspended = res.Suspended
		rs.forceIOFailures = res.ForceIOFailures

		for j := range res.Devices {
			dev := &res.Devices[j]
			ds := rs.getOrCreateDevice(dev.Volume)
			ds.minor = dev.Minor
			ds.diskState = dev.DiskState
			ds.client = dev.Client
			ds.open = dev.Open
			ds.quorum = dev.Quorum
			ds.sizeKiB = dev.Size
		}

		for j := range res.Connections {
			conn := &res.Connections[j]
			cs := rs.getOrCreateConnection(conn.PeerNodeID)
			cs.name = conn.Name
			cs.connectionState = conn.ConnectionState
			cs.peerRole = conn.Peerrole

			for k := range conn.Paths {
				path := &conn.Paths[k]
				localKey := fmt.Sprintf("ipv4:%s:%d", path.ThisHost.Address, path.ThisHost.Port)
				ps := cs.getOrCreatePath(localKey)
				ps.localIP = path.ThisHost.Address
				ps.localPort = path.ThisHost.Port
				ps.remoteIP = path.RemoteHost.Address
				ps.remotePort = path.RemoteHost.Port
				ps.established = path.Established
			}

			for k := range conn.PeerDevices {
				pd := &conn.PeerDevices[k]
				pds := cs.getOrCreatePeerDevice(pd.Volume)
				pds.replicationState = pd.ReplicationState
				pds.peerDiskState = pd.PeerDiskState
				pds.peerClient = pd.PeerClient
				pds.resyncSuspended = pd.ResyncSuspended
			}
		}
	}
}

// MarkReadyForTest marks the store as ready without going through the dump
// lifecycle. For testing only.
func (s *DRBDStateStore) MarkReadyForTest() {
	s.readyOnce.Do(func() { close(s.readyCh) })
}

// Parsing helpers

func parseBoolField(s string) bool {
	return s == "yes"
}

func parseIntStr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseIntField(state map[string]string, key string) int {
	return parseIntStr(state[key])
}

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
