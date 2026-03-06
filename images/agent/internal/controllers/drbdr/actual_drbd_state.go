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
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// ActualDRBDState represents the actual DRBD state observed from the system.
type ActualDRBDState interface {
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

	// QuorumDynamicVoters returns the quorum-dynamic-voters setting.
	QuorumDynamicVoters() bool

	// Volumes returns the list of volumes/devices for this resource.
	Volumes() []ActualVolume

	// Peers returns the list of peer connections for this resource.
	Peers() []ActualPeer

	// Report fills the status fields from the actual state.
	// Returns error if reporting invariants are violated (e.g., multiple volumes).
	// Even on error, it attempts to report the rest of the fields.
	// The drbdr is used to look up peer names from spec and access status.Addresses.
	Report(drbdr *v1alpha1.DRBDResource) error
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

	// Size returns the device size in bytes.
	Size() int64

	// DiscardZeroesIfAligned returns the discard-zeroes-if-aligned setting.
	DiscardZeroesIfAligned() bool

	// RsDiscardGranularity returns the rs-discard-granularity setting.
	RsDiscardGranularity() string

	// NonVoting returns the non-voting setting.
	NonVoting() bool
}

type ActualPeer interface {
	// NodeID returns the peer's node ID.
	NodeID() uint8

	// Name returns the peer's name.
	Name() string

	// ConnectionState returns the connection state (e.g., "Connected", "StandAlone").
	ConnectionState() string

	// PeerDiskState returns the peer's disk state for volume 0.
	PeerDiskState() string

	// Protocol returns the replication protocol (A, B, or C).
	Protocol() string

	// SharedSecret returns the shared secret for authentication.
	SharedSecret() string

	// SharedSecretAlg returns the HMAC algorithm for shared secret.
	SharedSecretAlg() string

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

func (aState *actualState) QuorumDynamicVoters() bool {
	if aState.show != nil {
		return aState.show.Options.QuorumDynamicVoters
	}
	return false
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
		// DRBD reports size in KiB, convert to bytes
		sizeBytes := int64(dev.Size) * 1024
		volumes = append(volumes, &actualVolume{
			minor:      dev.Minor,
			volumeNr:   dev.Volume,
			diskState:  dev.DiskState,
			hasQuorum:  dev.Quorum,
			sizeBytes:  sizeBytes,
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

func (aState *actualState) Report(drbdr *v1alpha1.DRBDResource) error {
	if aState == nil {
		return errors.New("unable to retrieve actual state")
	}

	status := &drbdr.Status

	if aState.status == nil && aState.show == nil {
		// Resource doesn't exist in DRBD - this is valid when resource is Down
		// Reset all fields except activeConfiguration.state
		status.Device = ""
		status.DiskState = ""
		status.Quorum = nil
		status.Peers = nil

		// Keep activeConfiguration but set state to Down
		if status.ActiveConfiguration == nil {
			status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
		}
		status.ActiveConfiguration.State = v1alpha1.DRBDResourceStateDown

		return nil
	}

	// Invariant check: we expect exactly one volume
	var err error
	var volumes []drbdsetup.Device
	if aState.status != nil {
		volumes = aState.status.Devices
	}

	if len(volumes) == 0 {
		err = errors.Join(err, errors.New("expected 1 volume, got 0"))
		// Clear volume-related fields to avoid obsolete state
		status.Device = ""
		status.DiskState = ""
		status.Quorum = nil
	} else {
		if len(volumes) > 1 {
			err = errors.Join(err, fmt.Errorf("expected 1 volume, got %d", len(volumes)))
		}

		vol := &volumes[0]
		status.Device = fmt.Sprintf("/dev/drbd%d", vol.Minor)
		status.DiskState = v1alpha1.DiskState(vol.DiskState)
		status.Quorum = &vol.Quorum
	}

	// Report ActiveConfiguration
	aState.reportActiveConfiguration(status, volumes)

	// Report Peers (including per-peer ReplicationState)
	aState.reportPeers(drbdr)

	return err
}

func (aState *actualState) reportActiveConfiguration(status *v1alpha1.DRBDResourceStatus, volumes []drbdsetup.Device) {
	if status.ActiveConfiguration == nil {
		status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
	}
	ac := status.ActiveConfiguration

	// State is Up if resource exists
	ac.State = v1alpha1.DRBDResourceStateUp

	// Role from status
	if aState.status != nil {
		ac.Role = v1alpha1.DRBDRole(aState.status.Role)
	} else {
		ac.Role = ""
	}

	// Quorum from resource options
	ac.Quorum = nil
	if aState.show != nil {
		quorumStr := aState.show.Options.Quorum
		if quorumStr != "" && quorumStr != "off" {
			if q, parseErr := strconv.ParseUint(quorumStr, 10, 8); parseErr == nil {
				qb := byte(q)
				ac.Quorum = &qb
			}
		}
	}

	// QuorumMinimumRedundancy from resource options
	ac.QuorumMinimumRedundancy = nil
	if aState.show != nil {
		qmrStr := aState.show.Options.QuorumMinimumRedundancy
		if qmrStr != "" && qmrStr != "off" {
			if q, parseErr := strconv.ParseUint(qmrStr, 10, 8); parseErr == nil {
				qb := byte(q)
				ac.QuorumMinimumRedundancy = &qb
			}
		}
	}

	// AllowTwoPrimaries - get from first connection in show
	ac.AllowTwoPrimaries = nil
	if aState.show != nil && len(aState.show.Connections) > 0 {
		atp := aState.show.Connections[0].Net.AllowTwoPrimaries
		ac.AllowTwoPrimaries = &atp
	}

	// NonVoting - get from first volume disk options in show
	ac.NonVoting = nil
	if aState.show != nil && len(aState.show.ThisHost.Volumes) > 0 {
		disk := &aState.show.ThisHost.Volumes[0].Disk
		if !disk.IsNone {
			nv := disk.NonVoting
			ac.NonVoting = &nv
		}
	}

	// Type and Size from first volume.
	// Type is determined from drbdsetup show configuration ("disk" field), not from
	// the transient disk state in drbdsetup status.
	//
	// drbdsetup show JSON output per volume:
	//   - Diskful:  has "backing-disk": "/dev/vg/lv", "disk": { ...options... }
	//   - Diskless: has "disk": "none" only (no "backing-disk" field at all)
	//   - Detached: has only "volume_nr" and "device_minor" (no "backing-disk", no "disk")
	//
	// Diskless is reported only when "disk" is explicitly the string "none"
	// (Disk.IsNone == true). Everything else — including detached diskful
	// resources where "backing-disk" is absent — is reported as Diskful,
	// because the configured type does not change when the backing device
	// is temporarily unavailable. The diskState field reports disk health.
	//
	// LVMLogicalVolumeName is set by the reconciler after reverse-lookup
	// from the backing disk path. This Report() method does not set it.
	if len(volumes) > 0 {
		vol := &volumes[0]

		// Look up the show volume to check disk configuration.
		diskIsNone := false
		if aState.show != nil {
			for i := range aState.show.ThisHost.Volumes {
				if aState.show.ThisHost.Volumes[i].VolumeNr == vol.Volume {
					diskIsNone = aState.show.ThisHost.Volumes[i].Disk.IsNone
					break
				}
			}
		}

		if diskIsNone {
			ac.Type = v1alpha1.DRBDResourceTypeDiskless
			ac.Size = nil
		} else {
			ac.Type = v1alpha1.DRBDResourceTypeDiskful
			// DRBD reports size in KiB, convert to bytes for resource.Quantity
			sizeBytes := int64(vol.Size) * 1024
			sizeQuantity := resource.NewQuantity(sizeBytes, resource.BinarySI)
			ac.Size = sizeQuantity
		}
	} else {
		// No volumes - clear volume-related fields in ac
		ac.Type = ""
		ac.Size = nil
	}
}

func (aState *actualState) reportPeers(drbdr *v1alpha1.DRBDResource) {
	status := &drbdr.Status

	if aState.status == nil || len(aState.status.Connections) == 0 {
		status.Peers = nil
		return
	}

	connections := aState.status.Connections
	newPeers := make([]v1alpha1.DRBDResourcePeerStatus, 0, len(connections))

	for i := range connections {
		conn := &connections[i]
		nodeID := uint(conn.PeerNodeID)

		// Look up peer name from spec by NodeID, fall back to actual connection name
		peerName := conn.Name
		for j := range drbdr.Spec.Peers {
			if uint(drbdr.Spec.Peers[j].NodeID) == nodeID {
				peerName = drbdr.Spec.Peers[j].Name
				break
			}
		}

		// Get peer disk state from first peer device
		var peerDiskState string
		if len(conn.PeerDevices) > 0 {
			peerDiskState = conn.PeerDevices[0].PeerDiskState
		}

		// Get replication state from first peer device
		var replicationState v1alpha1.ReplicationState
		if len(conn.PeerDevices) > 0 {
			raw := conn.PeerDevices[0].ReplicationState
			if raw != "" {
				replicationState = v1alpha1.ParseReplicationState(raw)
				if replicationState == "" {
					replicationState = v1alpha1.ReplicationStateUnknown
				}
			}
		}

		peerStatus := v1alpha1.DRBDResourcePeerStatus{
			Name:             peerName,
			NodeID:           nodeID,
			ConnectionState:  v1alpha1.ConnectionState(conn.ConnectionState),
			DiskState:        v1alpha1.DiskState(peerDiskState),
			ReplicationState: replicationState,
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
				// Look up SystemNetworkName from status.Addresses by IP
				addr, found := uiter.Find(slices.Values(status.Addresses), func(a v1alpha1.DRBDResourceAddressStatus) bool {
					return a.Address.IPv4 == path.ThisHost.Address
				})
				if !found {
					// Path IP not found in addresses - skip this path.
					// This can happen with stale data or manual DRBD configuration.
					continue
				}
				peerStatus.Paths = append(peerStatus.Paths, v1alpha1.DRBDResourcePathStatus{
					SystemNetworkName: addr.SystemNetworkName,
					Address: v1alpha1.DRBDAddress{
						IPv4: path.ThisHost.Address,
						Port: uint(path.ThisHost.Port),
					},
					Established: path.Established,
				})
			}
		}

		newPeers = append(newPeers, peerStatus)
	}

	slices.SortStableFunc(
		newPeers,
		func(a v1alpha1.DRBDResourcePeerStatus, b v1alpha1.DRBDResourcePeerStatus) int {
			return strings.Compare(a.Name, b.Name)
		},
	)

	status.Peers = newPeers
}

var _ ActualDRBDState = &actualState{}

// actualVolume implements ActualVolume.
type actualVolume struct {
	minor      int
	volumeNr   int
	diskState  string
	hasQuorum  bool
	sizeBytes  int64
	showVolume *drbdsetup.ShowVolume
}

func (v *actualVolume) Minor() int      { return v.minor }
func (v *actualVolume) VolumeNr() int   { return v.volumeNr }
func (v *actualVolume) HasQuorum() bool { return v.hasQuorum }
func (v *actualVolume) Size() int64     { return v.sizeBytes }
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

func (v *actualVolume) NonVoting() bool {
	if v.showVolume != nil {
		return v.showVolume.Disk.NonVoting
	}
	return false
}

var _ ActualVolume = &actualVolume{}

// actualPeer implements ActualPeer.
type actualPeer struct {
	connection     *drbdsetup.Connection
	showConnection *drbdsetup.ShowConnection
}

func (p *actualPeer) NodeID() uint8 {
	return uint8(p.connection.PeerNodeID)
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

func (p *actualPeer) Protocol() string {
	if p.showConnection != nil {
		return p.showConnection.Net.Protocol
	}
	return ""
}

func (p *actualPeer) SharedSecret() string {
	if p.showConnection != nil {
		return p.showConnection.Net.SharedSecret
	}
	return ""
}

func (p *actualPeer) SharedSecretAlg() string {
	if p.showConnection != nil {
		return p.showConnection.Net.CRAMHMACAlg
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

// observeActualDRBDState retrieves the actual DRBD state by querying drbdsetup.
func observeActualDRBDState(ctx context.Context, drbdResName string) (*actualState, error) {
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
