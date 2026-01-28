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

package drbd

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type ActualState interface {
	IsZero() bool

	// ResourceName returns the DRBD resource name.
	ResourceName() string

	// ResourceExists returns true if the resource exists in DRBD.
	ResourceExists() bool

	// NodeID returns this node's ID for the resource.
	NodeID() uint

	// Role returns the role of this node (e.g., "Primary", "Secondary").
	Role() string

	// AutoPromote returns the auto-promote setting.
	AutoPromote() bool

	// Quorum returns the quorum setting (e.g., "off", "1", "2", etc.).
	Quorum() string

	// QuorumMinimumRedundancy returns the quorum-minimum-redundancy setting.
	QuorumMinimumRedundancy() string

	// OnNoQuorum returns the on-no-quorum action.
	OnNoQuorum() string

	// OnNoDataAccessible returns the on-no-data-accessible action.
	OnNoDataAccessible() string

	// OnSuspendedPrimaryOutdated returns the on-suspended-primary-outdated action.
	OnSuspendedPrimaryOutdated() string

	// Volumes returns the list of volumes/devices for this resource.
	Volumes() []ActualVolume

	// Peers returns the list of peer connections for this resource.
	Peers() []ActualPeer

	// Report fills the status fields from the actual state.
	// Returns true if any field was changed.
	// Returns error if reporting invariants are violated (e.g., multiple volumes).
	// Even on error, it attempts to report the rest of the fields.
	Report(status *v1alpha1.DRBDResourceStatus) (changed bool, err error)
}

type ActualVolume interface {
	// Minor returns the device minor number.
	Minor() int

	// VolumeNr returns the volume number within the resource.
	VolumeNr() int

	// BackingDisk returns the path to the backing device.
	BackingDisk() string

	// DiskState returns the disk state (e.g., "UpToDate", "Diskless").
	DiskState() string

	// HasQuorum returns true if this volume has quorum.
	HasQuorum() bool

	// DiscardZeroesIfAligned returns the discard-zeroes-if-aligned setting.
	DiscardZeroesIfAligned() bool

	// RsDiscardGranularity returns the rs-discard-granularity setting.
	RsDiscardGranularity() string
}

type ActualPeer interface {
	// NodeID returns the peer's node ID.
	NodeID() uint

	// Name returns the peer's name.
	Name() string

	// ConnectionState returns the connection state (e.g., "Connected", "StandAlone").
	ConnectionState() string

	// PeerDiskState returns the peer's disk state for volume 0.
	PeerDiskState() string

	// AllowTwoPrimaries returns the allow-two-primaries setting.
	AllowTwoPrimaries() bool

	// AllowRemoteRead returns the allow-remote-read setting.
	AllowRemoteRead() bool

	// Paths returns the network paths to this peer.
	Paths() []ActualPath
}

type ActualPath interface {
	// LocalAddr returns the local address in "ip:port" format.
	LocalAddr() string

	// RemoteAddr returns the remote address in "ip:port" format.
	RemoteAddr() string

	// Established returns true if the path is established.
	Established() bool
}

// actualState represents the observed DRBD resource state.
type actualState struct {
	status *drbdsetup.Resource
	show   *drbdsetup.ShowResource
}

func (aState *actualState) IsZero() bool {
	return aState == nil
}

func (aState *actualState) ResourceName() string {
	if aState.status != nil {
		return aState.status.Name
	}
	if aState.show != nil {
		return aState.show.Resource
	}
	return ""
}

func (aState *actualState) ResourceExists() bool {
	return aState.status != nil || aState.show != nil
}

func (aState *actualState) NodeID() uint {
	if aState.show != nil {
		return uint(aState.show.ThisHost.NodeID)
	}
	if aState.status != nil {
		return uint(aState.status.NodeID)
	}
	return 0
}

func (aState *actualState) Role() string {
	if aState.status != nil {
		return aState.status.Role
	}
	return ""
}

func (aState *actualState) AutoPromote() bool {
	if aState.show != nil {
		return aState.show.Options.AutoPromote
	}
	return false
}

func (aState *actualState) Quorum() string {
	if aState.show != nil {
		return aState.show.Options.Quorum
	}
	return ""
}

func (aState *actualState) QuorumMinimumRedundancy() string {
	if aState.show != nil {
		return aState.show.Options.QuorumMinimumRedundancy
	}
	return ""
}

func (aState *actualState) OnNoQuorum() string {
	if aState.show != nil {
		return aState.show.Options.OnNoQuorum
	}
	return ""
}

func (aState *actualState) OnNoDataAccessible() string {
	if aState.show != nil {
		return aState.show.Options.OnNoDataAccessible
	}
	return ""
}

func (aState *actualState) OnSuspendedPrimaryOutdated() string {
	if aState.show != nil {
		return aState.show.Options.OnSuspendedPrimaryOutdated
	}
	return ""
}

func (aState *actualState) Volumes() []ActualVolume {
	if aState.status == nil {
		return nil
	}

	// Build a map of show volumes by volume number
	showVolumes := make(map[int]*drbdsetup.ShowVolume)
	if aState.show != nil {
		for i := range aState.show.ThisHost.Volumes {
			vol := &aState.show.ThisHost.Volumes[i]
			showVolumes[vol.VolumeNr] = vol
		}
	}

	volumes := make([]ActualVolume, 0, len(aState.status.Devices))
	for i := range aState.status.Devices {
		dev := &aState.status.Devices[i]
		volumes = append(volumes, &actualVolume{
			minor:      dev.Minor,
			volumeNr:   dev.Volume,
			diskState:  dev.DiskState,
			hasQuorum:  dev.Quorum,
			showVolume: showVolumes[dev.Volume],
		})
	}
	return volumes
}

func (aState *actualState) Peers() []ActualPeer {
	if aState.status == nil {
		return nil
	}

	// Build a map of show connections by peer node ID
	showConnections := make(map[int]*drbdsetup.ShowConnection)
	if aState.show != nil {
		for i := range aState.show.Connections {
			conn := &aState.show.Connections[i]
			showConnections[conn.PeerNodeID] = conn
		}
	}

	peers := make([]ActualPeer, 0, len(aState.status.Connections))
	for i := range aState.status.Connections {
		conn := &aState.status.Connections[i]
		peers = append(peers, &actualPeer{
			connection:     conn,
			showConnection: showConnections[conn.PeerNodeID],
		})
	}
	return peers
}

func (aState *actualState) Report(status *v1alpha1.DRBDResourceStatus) (changed bool, err error) {
	if aState == nil {
		return false, errors.New("unable to retrieve actual state")
	}

	if aState.status == nil && aState.show == nil {
		// Resource doesn't exist in DRBD - broken invariant at report stage
		err = errors.New("resource not found in DRBD")

		// Reset all fields except activeConfiguration.state
		if status.Device != "" {
			status.Device = ""
			changed = true
		}
		if status.DiskState != "" {
			status.DiskState = ""
			changed = true
		}
		if status.Quorum != nil {
			status.Quorum = nil
			changed = true
		}
		if len(status.Peers) > 0 {
			status.Peers = nil
			changed = true
		}

		// Keep activeConfiguration but set state to Down
		if status.ActiveConfiguration == nil {
			status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
			changed = true
		}
		if status.ActiveConfiguration.State != v1alpha1.DRBDResourceStateDown {
			status.ActiveConfiguration.State = v1alpha1.DRBDResourceStateDown
			changed = true
		}

		return changed, err
	}

	// Invariant check: we expect exactly one volume
	var volumes []drbdsetup.Device
	if aState.status != nil {
		volumes = aState.status.Devices
	}

	if len(volumes) == 0 {
		err = errors.Join(err, errors.New("expected 1 volume, got 0"))
		// Clear volume-related fields to avoid obsolete state
		if status.Device != "" {
			status.Device = ""
			changed = true
		}
		if status.DiskState != "" {
			status.DiskState = ""
			changed = true
		}
		if status.Quorum != nil {
			status.Quorum = nil
			changed = true
		}
	} else {
		if len(volumes) > 1 {
			err = errors.Join(err, fmt.Errorf("expected 1 volume, got %d", len(volumes)))
		}

		vol := &volumes[0]
		device := fmt.Sprintf("/dev/drbd%d", vol.Minor)
		if status.Device != device {
			status.Device = device
			changed = true
		}

		diskState := v1alpha1.DiskState(vol.DiskState)
		if status.DiskState != diskState {
			status.DiskState = diskState
			changed = true
		}

		if !pointerEqual(status.Quorum, &vol.Quorum) {
			status.Quorum = &vol.Quorum
			changed = true
		}
	}

	// Report ActiveConfiguration
	c, e := aState.reportActiveConfiguration(status, volumes)
	changed = c || changed
	err = errors.Join(err, e)

	// Report Peers
	c = aState.reportPeers(status)
	changed = c || changed

	return changed, err
}

func (aState *actualState) reportActiveConfiguration(status *v1alpha1.DRBDResourceStatus, volumes []drbdsetup.Device) (changed bool, err error) {
	if status.ActiveConfiguration == nil {
		status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
		changed = true
	}
	ac := status.ActiveConfiguration

	// State is Up if resource exists
	if ac.State != v1alpha1.DRBDResourceStateUp {
		ac.State = v1alpha1.DRBDResourceStateUp
		changed = true
	}

	// Role from status
	var role v1alpha1.DRBDRole
	if aState.status != nil {
		role = v1alpha1.DRBDRole(aState.status.Role)
	}
	if ac.Role != role {
		ac.Role = role
		changed = true
	}

	// Quorum from resource options
	var quorumVal *byte
	if aState.show != nil {
		quorumStr := aState.show.Options.Quorum
		if quorumStr != "" && quorumStr != "off" {
			if q, parseErr := strconv.ParseUint(quorumStr, 10, 8); parseErr == nil {
				qb := byte(q)
				quorumVal = &qb
			}
		}
	}
	if !pointerEqual(ac.Quorum, quorumVal) {
		ac.Quorum = quorumVal
		changed = true
	}

	// QuorumMinimumRedundancy from resource options
	var qmrVal *byte
	if aState.show != nil {
		qmrStr := aState.show.Options.QuorumMinimumRedundancy
		if qmrStr != "" && qmrStr != "off" {
			if q, parseErr := strconv.ParseUint(qmrStr, 10, 8); parseErr == nil {
				qb := byte(q)
				qmrVal = &qb
			}
		}
	}
	if !pointerEqual(ac.QuorumMinimumRedundancy, qmrVal) {
		ac.QuorumMinimumRedundancy = qmrVal
		changed = true
	}

	// AllowTwoPrimaries - get from first connection in show
	var allowTwoPrimaries *bool
	if aState.show != nil && len(aState.show.Connections) > 0 {
		atp := aState.show.Connections[0].Net.AllowTwoPrimaries
		allowTwoPrimaries = &atp
	}
	if !pointerEqual(ac.AllowTwoPrimaries, allowTwoPrimaries) {
		ac.AllowTwoPrimaries = allowTwoPrimaries
		changed = true
	}

	// Type and Disk from first volume
	if len(volumes) > 0 {
		vol := &volumes[0]

		var resType v1alpha1.DRBDResourceType
		if vol.DiskState == "Diskless" {
			resType = v1alpha1.DRBDResourceTypeDiskless
		} else {
			resType = v1alpha1.DRBDResourceTypeDiskful
		}
		if ac.Type != resType {
			ac.Type = resType
			changed = true
		}

		// Backing disk from show volumes
		var backingDisk string
		if aState.show != nil {
			for i := range aState.show.ThisHost.Volumes {
				if aState.show.ThisHost.Volumes[i].VolumeNr == vol.Volume {
					backingDisk = aState.show.ThisHost.Volumes[i].BackingDisk
					break
				}
			}
		}
		if ac.Disk != backingDisk {
			ac.Disk = backingDisk
			changed = true
		}
	} else {
		// No volumes - clear volume-related fields in ac
		if ac.Type != "" {
			ac.Type = ""
			changed = true
		}
		if ac.Disk != "" {
			ac.Disk = ""
			changed = true
		}
	}

	return changed, err
}

func (aState *actualState) reportPeers(status *v1alpha1.DRBDResourceStatus) (changed bool) {
	if aState.status == nil || len(aState.status.Connections) == 0 {
		if len(status.Peers) > 0 {
			status.Peers = nil
			return true
		}
		return false
	}

	connections := aState.status.Connections

	// Build a map of existing peers by name for efficient lookup
	existingPeers := make(map[string]*v1alpha1.DRBDResourcePeerStatus)
	for i := range status.Peers {
		existingPeers[status.Peers[i].Name] = &status.Peers[i]
	}

	newPeers := make([]v1alpha1.DRBDResourcePeerStatus, 0, len(connections))
	for i := range connections {
		conn := &connections[i]
		peerName := conn.Name
		nodeID := uint(conn.PeerNodeID)

		// Get peer disk state from first peer device
		var peerDiskState string
		if len(conn.PeerDevices) > 0 {
			peerDiskState = conn.PeerDevices[0].PeerDiskState
		}

		peerStatus := v1alpha1.DRBDResourcePeerStatus{
			Name:            peerName,
			NodeID:          &nodeID,
			ConnectionState: v1alpha1.ConnectionState(conn.ConnectionState),
			DiskState:       v1alpha1.DiskState(peerDiskState),
		}

		// Determine type from disk state
		if peerDiskState == "Diskless" {
			peerStatus.Type = v1alpha1.DRBDResourceTypeDiskless
		} else if peerDiskState != "" {
			peerStatus.Type = v1alpha1.DRBDResourceTypeDiskful
		}

		// Build paths
		if len(conn.Paths) > 0 {
			peerStatus.Paths = make([]v1alpha1.DRBDResourcePathStatus, 0, len(conn.Paths))
			for j := range conn.Paths {
				path := &conn.Paths[j]
				peerStatus.Paths = append(peerStatus.Paths, v1alpha1.DRBDResourcePathStatus{
					SystemNetworkName: "",
					Address: v1alpha1.DRBDAddress{
						IPv4: path.ThisHost.Address,
						Port: uint(path.ThisHost.Port),
					},
					Established: path.Established,
				})
			}
		}

		newPeers = append(newPeers, peerStatus)

		// Check if this peer changed
		if existing, ok := existingPeers[peerName]; !ok || !peerStatusEqual(existing, &peerStatus) {
			changed = true
		}
	}

	// Check for removed peers
	if len(newPeers) != len(status.Peers) {
		changed = true
	}

	if changed {
		status.Peers = newPeers
	}

	return changed
}

func pointerEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func peerStatusEqual(a, b *v1alpha1.DRBDResourcePeerStatus) bool {
	if a.Name != b.Name {
		return false
	}
	if !pointerEqual(a.NodeID, b.NodeID) {
		return false
	}
	if a.ConnectionState != b.ConnectionState {
		return false
	}
	if a.DiskState != b.DiskState {
		return false
	}
	if a.Type != b.Type {
		return false
	}
	if len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		if !pathStatusEqual(&a.Paths[i], &b.Paths[i]) {
			return false
		}
	}
	return true
}

func pathStatusEqual(a, b *v1alpha1.DRBDResourcePathStatus) bool {
	return a.SystemNetworkName == b.SystemNetworkName &&
		a.Address.IPv4 == b.Address.IPv4 &&
		a.Address.Port == b.Address.Port &&
		a.Established == b.Established
}

var _ ActualState = &actualState{}

// actualVolume implements ActualVolume.
type actualVolume struct {
	minor      int
	volumeNr   int
	diskState  string
	hasQuorum  bool
	showVolume *drbdsetup.ShowVolume
}

func (v *actualVolume) Minor() int      { return v.minor }
func (v *actualVolume) VolumeNr() int   { return v.volumeNr }
func (v *actualVolume) HasQuorum() bool { return v.hasQuorum }
func (v *actualVolume) BackingDisk() string {
	if v.showVolume != nil {
		return v.showVolume.BackingDisk
	}
	return ""
}
func (v *actualVolume) DiskState() string { return v.diskState }
func (v *actualVolume) DiscardZeroesIfAligned() bool {
	if v.showVolume != nil {
		return v.showVolume.Disk.DiscardZeroesIfAligned
	}
	return false
}
func (v *actualVolume) RsDiscardGranularity() string {
	if v.showVolume != nil {
		return v.showVolume.Disk.RSDiscardGranularity
	}
	return ""
}

var _ ActualVolume = &actualVolume{}

// actualPeer implements ActualPeer.
type actualPeer struct {
	connection     *drbdsetup.Connection
	showConnection *drbdsetup.ShowConnection
}

func (p *actualPeer) NodeID() uint {
	return uint(p.connection.PeerNodeID)
}

func (p *actualPeer) Name() string {
	return p.connection.Name
}

func (p *actualPeer) ConnectionState() string {
	return p.connection.ConnectionState
}

func (p *actualPeer) PeerDiskState() string {
	if len(p.connection.PeerDevices) > 0 {
		return p.connection.PeerDevices[0].PeerDiskState
	}
	return ""
}

func (p *actualPeer) AllowTwoPrimaries() bool {
	if p.showConnection != nil {
		return p.showConnection.Net.AllowTwoPrimaries
	}
	return false
}

func (p *actualPeer) AllowRemoteRead() bool {
	if p.showConnection != nil {
		return p.showConnection.Net.AllowRemoteRead
	}
	return false
}

func (p *actualPeer) Paths() []ActualPath {
	paths := make([]ActualPath, 0, len(p.connection.Paths))
	for i := range p.connection.Paths {
		path := &p.connection.Paths[i]
		paths = append(paths, &actualPath{path: path})
	}
	return paths
}

var _ ActualPeer = &actualPeer{}

// actualPath implements ActualPath.
type actualPath struct {
	path *drbdsetup.Path
}

func (p *actualPath) LocalAddr() string {
	return fmt.Sprintf("%s:%d", p.path.ThisHost.Address, p.path.ThisHost.Port)
}

func (p *actualPath) RemoteAddr() string {
	return fmt.Sprintf("%s:%d", p.path.RemoteHost.Address, p.path.RemoteHost.Port)
}

func (p *actualPath) Established() bool {
	return p.path.Established
}

var _ ActualPath = &actualPath{}

func getActualState(ctx context.Context, drbdResName string) (*actualState, error) {
	statusResult, err := drbdsetup.ExecuteStatus(ctx, drbdResName)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup status: %w", err)
	}

	if len(statusResult) != 1 {
		// Resource not found in DRBD status - it's not configured yet.
		// Return a valid (non-nil) state with ResourceExists() == false.
		return &actualState{}, nil
	}

	// Get show output for configuration details
	showResults, err := drbdsetup.ExecuteShow(ctx, drbdResName, true)
	if err != nil {
		return nil, fmt.Errorf("executing drbdsetup show: %w", err)
	}

	var showResult *drbdsetup.ShowResource
	for i := range showResults {
		if showResults[i].Resource == drbdResName {
			showResult = &showResults[i]
			break
		}
	}

	return &actualState{
		status: &statusResult[0],
		show:   showResult,
	}, nil
}
