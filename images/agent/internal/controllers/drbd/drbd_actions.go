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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdmeta"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// DRBDAction represents a DRBD command to execute.
type DRBDAction interface {
	Execute(ctx context.Context) error
}

// DRBDActions is a list of DRBD actions to execute.
type DRBDActions []DRBDAction

//
// DRBDAction implementations
//

// NewResourceAction creates a new DRBD resource.
type NewResourceAction struct {
	ResourceName string
	NodeID       uint
}

func (a NewResourceAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNewResource(ctx, a.ResourceName, a.NodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewResourceFailed)
}

// ResourceOptionsAction sets resource-level options.
type ResourceOptionsAction struct {
	ResourceName               string
	AutoPromote                *bool
	OnNoQuorum                 string
	OnNoDataAccessible         string
	OnSuspendedPrimaryOutdated string
	Quorum                     *uint
	QuorumMinimumRedundancy    *uint
}

func (a ResourceOptionsAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteResourceOptions(ctx, a.ResourceName, drbdsetup.ResourceOptions{
		AutoPromote:                a.AutoPromote,
		OnNoQuorum:                 a.OnNoQuorum,
		OnNoDataAccessible:         a.OnNoDataAccessible,
		OnSuspendedPrimaryOutdated: a.OnSuspendedPrimaryOutdated,
		Quorum:                     a.Quorum,
		QuorumMinimumRedundancy:    a.QuorumMinimumRedundancy,
	})
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonResourceOptionsFailed)
}

// NewMinorAction creates a new DRBD device/volume within a resource.
type NewMinorAction struct {
	ResourceName   string
	Volume         uint
	AllocatedMinor *uint
}

func (a NewMinorAction) Execute(ctx context.Context) error {
	minor, err := drbdsetup.ExecuteNewAutoMinor(ctx, a.ResourceName, a.Volume)
	if err != nil {
		return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewMinorFailed)
	}
	if a.AllocatedMinor != nil {
		*a.AllocatedMinor = minor
	}
	return nil
}

// CreateMetadataAction creates DRBD metadata on a backing device.
type CreateMetadataAction struct {
	Minor      *uint
	BackingDev string
}

func (a CreateMetadataAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return ConfiguredReasonError(
			fmt.Errorf("CreateMetadataAction: minor not set"),
			v1alpha1.DRBDResourceCondConfiguredReasonCreateMetadataFailed,
		)
	}
	err := drbdmeta.ExecuteCreateMD(ctx, *a.Minor, a.BackingDev)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonCreateMetadataFailed)
}

// AttachAction attaches a backing device to a volume.
type AttachAction struct {
	Minor    *uint
	LowerDev string // Path to backing block device
	MetaDev  string // Path to meta-data device or "internal"
	MetaIdx  string // Meta-data index or "internal"/"flexible"
}

func (a AttachAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return ConfiguredReasonError(
			fmt.Errorf("AttachAction: minor not set"),
			v1alpha1.DRBDResourceCondConfiguredReasonAttachFailed,
		)
	}
	err := drbdsetup.ExecuteAttach(ctx, *a.Minor, a.LowerDev, a.MetaDev, a.MetaIdx)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonAttachFailed)
}

// DiskOptionsAction sets disk options on an attached volume.
type DiskOptionsAction struct {
	Minor                  *uint
	DiscardZeroesIfAligned *bool
	RsDiscardGranularity   *uint
}

func (a DiskOptionsAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return ConfiguredReasonError(
			fmt.Errorf("DiskOptionsAction: minor not set"),
			v1alpha1.DRBDResourceCondConfiguredReasonDiskOptionsFailed,
		)
	}
	err := drbdsetup.ExecuteDiskOptions(ctx, *a.Minor, drbdsetup.DiskOptions{
		DiscardZeroesIfAligned: a.DiscardZeroesIfAligned,
		RsDiscardGranularity:   a.RsDiscardGranularity,
	})
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDiskOptionsFailed)
}

// NewPeerAction makes a peer node known to the resource.
type NewPeerAction struct {
	ResourceName string
	PeerNodeID   uint
	Protocol     string // A, B, or C
	SharedSecret string
	CRAMHMACAlg  string // HMAC algorithm for authentication
	RRConflict   string // e.g., "retry-connect"
}

func (a NewPeerAction) Execute(ctx context.Context) error {
	var opts *drbdsetup.NewPeerOptions
	if a.Protocol != "" || a.SharedSecret != "" || a.CRAMHMACAlg != "" || a.RRConflict != "" {
		opts = &drbdsetup.NewPeerOptions{
			Protocol:     a.Protocol,
			SharedSecret: a.SharedSecret,
			CRAMHMACAlg:  a.CRAMHMACAlg,
			RRConflict:   a.RRConflict,
		}
	}
	err := drbdsetup.ExecuteNewPeer(ctx, a.ResourceName, a.PeerNodeID, opts)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewPeerFailed)
}

// NetOptionsAction sets network options on a peer connection.
type NetOptionsAction struct {
	ResourceName      string
	PeerNodeID        uint
	AllowTwoPrimaries *bool
	AllowRemoteRead   *bool
}

func (a NetOptionsAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNetOptions(ctx, a.ResourceName, a.PeerNodeID, drbdsetup.NetOptions{
		AllowTwoPrimaries: a.AllowTwoPrimaries,
		AllowRemoteRead:   a.AllowRemoteRead,
	})
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNetOptionsFailed)
}

// NewPathAction adds a network path to a peer.
type NewPathAction struct {
	ResourceName string
	PeerNodeID   uint
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a NewPathAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNewPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewPathFailed)
}

// ConnectAction establishes connection to a peer.
type ConnectAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a ConnectAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteConnect(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonConnectFailed)
}

// DisconnectAction disconnects from a peer.
type DisconnectAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a DisconnectAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDisconnect(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDisconnectFailed)
}

// DelPathAction removes a network path from a peer.
type DelPathAction struct {
	ResourceName string
	PeerNodeID   uint
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a DelPathAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDelPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDelPathFailed)
}

// DelPeerAction removes a peer connection.
type DelPeerAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a DelPeerAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDelPeer(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDelPeerFailed)
}

// DownAction tears down a DRBD resource completely.
type DownAction struct {
	ResourceName string
}

func (a DownAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDown(ctx, a.ResourceName)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDownFailed)
}
