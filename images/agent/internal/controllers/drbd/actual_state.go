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
	"fmt"

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

	// DiscardZeroesIfAligned returns the discard-zeroes-if-aligned setting.
	DiscardZeroesIfAligned() bool

	// RsDiscardGranularity returns the rs-discard-granularity setting.
	RsDiscardGranularity() string
}

type ActualPeer interface {
	// NodeID returns the peer's node ID.
	NodeID() uint

	// ConnectionState returns the connection state (e.g., "Connected", "StandAlone").
	ConnectionState() string

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

var _ ActualState = &actualState{}

// actualVolume implements ActualVolume.
type actualVolume struct {
	minor      int
	volumeNr   int
	diskState  string
	showVolume *drbdsetup.ShowVolume
}

func (v *actualVolume) Minor() int    { return v.minor }
func (v *actualVolume) VolumeNr() int { return v.volumeNr }
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

func (p *actualPeer) ConnectionState() string {
	return p.connection.ConnectionState
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
