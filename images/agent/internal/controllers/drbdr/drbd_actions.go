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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdmeta"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// DRBDAction represents a DRBD command to execute.
type DRBDAction interface {
	Execute(ctx context.Context) error
	String() string
}

// DRBDActions is a list of DRBD actions to execute.
type DRBDActions []DRBDAction

//
// DRBDAction implementations
//

// NewResourceAction creates a new DRBD resource.
type NewResourceAction struct {
	ResourceName string
	NodeID       uint8
}

func (a NewResourceAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNewResource(ctx, a.ResourceName, a.NodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewResourceFailed)
}

func (a NewResourceAction) String() string {
	return fmt.Sprintf("NewResource(resource=%s, nodeID=%d)", a.ResourceName, a.NodeID)
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

func (a ResourceOptionsAction) String() string {
	return fmt.Sprintf("ResourceOptions(resource=%s)", a.ResourceName)
}

// NewMinorAction creates a new DRBD device/volume within a resource.
type NewMinorAction struct {
	ResourceName   string
	Volume         uint
	Diskless       bool
	AllocatedMinor *uint
}

func (a NewMinorAction) Execute(ctx context.Context) error {
	minor, err := drbdsetup.ExecuteNewAutoMinor(ctx, a.ResourceName, a.Volume, a.Diskless)
	if err != nil {
		return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewMinorFailed)
	}
	if a.AllocatedMinor != nil {
		*a.AllocatedMinor = minor
	}
	return nil
}

func (a NewMinorAction) String() string {
	return fmt.Sprintf("NewMinor(resource=%s, volume=%d, diskless=%t)", a.ResourceName, a.Volume, a.Diskless)
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

func (a CreateMetadataAction) String() string {
	minor := "<nil>"
	if a.Minor != nil {
		minor = fmt.Sprintf("%d", *a.Minor)
	}
	return fmt.Sprintf("CreateMetadata(minor=%s, backingDev=%s)", minor, a.BackingDev)
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

func (a AttachAction) String() string {
	minor := "<nil>"
	if a.Minor != nil {
		minor = fmt.Sprintf("%d", *a.Minor)
	}
	return fmt.Sprintf("Attach(minor=%s, lowerDev=%s)", minor, a.LowerDev)
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

func (a DiskOptionsAction) String() string {
	minor := "<nil>"
	if a.Minor != nil {
		minor = fmt.Sprintf("%d", *a.Minor)
	}
	return fmt.Sprintf("DiskOptions(minor=%s)", minor)
}

// NewPeerAction makes a peer node known to the resource.
type NewPeerAction struct {
	ResourceName string
	PeerNodeID   uint8
	PeerName     string // Connection name (mandatory for --_name)
	Protocol     string // A, B, or C
	SharedSecret string
	CRAMHMACAlg  string // HMAC algorithm for authentication
	RRConflict   string // e.g., "retry-connect"
}

func (a NewPeerAction) Execute(ctx context.Context) error {
	opts := &drbdsetup.NewPeerOptions{
		Name:         a.PeerName,
		Protocol:     a.Protocol,
		SharedSecret: a.SharedSecret,
		CRAMHMACAlg:  a.CRAMHMACAlg,
		RRConflict:   a.RRConflict,
	}
	err := drbdsetup.ExecuteNewPeer(ctx, a.ResourceName, a.PeerNodeID, opts)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewPeerFailed)
}

func (a NewPeerAction) String() string {
	return fmt.Sprintf("NewPeer(resource=%s, peerNodeID=%d, peerName=%s)", a.ResourceName, a.PeerNodeID, a.PeerName)
}

// NetOptionsAction sets network options on a peer connection.
type NetOptionsAction struct {
	ResourceName      string
	PeerNodeID        uint8
	Protocol          *string
	SharedSecret      *string
	CRAMHMACAlg       *string
	AllowTwoPrimaries *bool
	AllowRemoteRead   *bool
}

func (a NetOptionsAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNetOptions(ctx, a.ResourceName, a.PeerNodeID, drbdsetup.NetOptions{
		Protocol:          a.Protocol,
		SharedSecret:      a.SharedSecret,
		CRAMHMACAlg:       a.CRAMHMACAlg,
		AllowTwoPrimaries: a.AllowTwoPrimaries,
		AllowRemoteRead:   a.AllowRemoteRead,
	})
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNetOptionsFailed)
}

func (a NetOptionsAction) String() string {
	return fmt.Sprintf("NetOptions(resource=%s, peerNodeID=%d)", a.ResourceName, a.PeerNodeID)
}

// NewPathAction adds a network path to a peer.
type NewPathAction struct {
	ResourceName string
	PeerNodeID   uint8
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a NewPathAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteNewPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonNewPathFailed)
}

func (a NewPathAction) String() string {
	return fmt.Sprintf("NewPath(resource=%s, peerNodeID=%d, local=%s, remote=%s)", a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
}

// ConnectAction establishes connection to a peer.
type ConnectAction struct {
	ResourceName string
	PeerNodeID   uint8
}

func (a ConnectAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteConnect(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonConnectFailed)
}

func (a ConnectAction) String() string {
	return fmt.Sprintf("Connect(resource=%s, peerNodeID=%d)", a.ResourceName, a.PeerNodeID)
}

// DisconnectAction disconnects from a peer.
type DisconnectAction struct {
	ResourceName string
	PeerNodeID   uint8
}

func (a DisconnectAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDisconnect(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDisconnectFailed)
}

func (a DisconnectAction) String() string {
	return fmt.Sprintf("Disconnect(resource=%s, peerNodeID=%d)", a.ResourceName, a.PeerNodeID)
}

// DelPathAction removes a network path from a peer.
type DelPathAction struct {
	ResourceName string
	PeerNodeID   uint8
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a DelPathAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDelPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDelPathFailed)
}

func (a DelPathAction) String() string {
	return fmt.Sprintf("DelPath(resource=%s, peerNodeID=%d, local=%s, remote=%s)", a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
}

// DelPeerAction removes a peer connection.
type DelPeerAction struct {
	ResourceName string
	PeerNodeID   uint8
}

func (a DelPeerAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDelPeer(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDelPeerFailed)
}

func (a DelPeerAction) String() string {
	return fmt.Sprintf("DelPeer(resource=%s, peerNodeID=%d)", a.ResourceName, a.PeerNodeID)
}

// ForgetPeerAction removes all references to a peer from meta-data.
type ForgetPeerAction struct {
	ResourceName string
	PeerNodeID   uint8
}

func (a ForgetPeerAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteForgetPeer(ctx, a.ResourceName, a.PeerNodeID)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonForgetPeerFailed)
}

func (a ForgetPeerAction) String() string {
	return fmt.Sprintf("ForgetPeer(resource=%s, peerNodeID=%d)", a.ResourceName, a.PeerNodeID)
}

// DownAction tears down a DRBD resource completely.
type DownAction struct {
	ResourceName string
}

func (a DownAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteDown(ctx, a.ResourceName)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDownFailed)
}

func (a DownAction) String() string {
	return fmt.Sprintf("Down(resource=%s)", a.ResourceName)
}

// DetachAction detaches the backing device from a volume.
type DetachAction struct {
	Minor *uint
}

func (a DetachAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return ConfiguredReasonError(
			fmt.Errorf("DetachAction: minor not set"),
			v1alpha1.DRBDResourceCondConfiguredReasonDetachFailed,
		)
	}
	err := drbdsetup.ExecuteDetach(ctx, *a.Minor)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonDetachFailed)
}

func (a DetachAction) String() string {
	minor := "<nil>"
	if a.Minor != nil {
		minor = fmt.Sprintf("%d", *a.Minor)
	}
	return fmt.Sprintf("Detach(minor=%s)", minor)
}

// RenameAction renames a DRBD resource locally.
type RenameAction struct {
	OldName string
	NewName string
}

func (a RenameAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteRename(ctx, a.OldName, a.NewName)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonRenameFailed)
}

func (a RenameAction) String() string {
	return fmt.Sprintf("Rename(from=%s, to=%s)", a.OldName, a.NewName)
}

// PrimaryAction promotes a DRBD resource to primary role.
type PrimaryAction struct {
	ResourceName string
	Force        bool
}

func (a PrimaryAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecutePrimary(ctx, a.ResourceName, a.Force)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonPrimaryFailed)
}

func (a PrimaryAction) String() string {
	return fmt.Sprintf("Primary(resource=%s, force=%t)", a.ResourceName, a.Force)
}

// SecondaryAction demotes a DRBD resource to secondary role.
type SecondaryAction struct {
	ResourceName string
	Force        bool
}

func (a SecondaryAction) Execute(ctx context.Context) error {
	err := drbdsetup.ExecuteSecondary(ctx, a.ResourceName, a.Force)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonSecondaryFailed)
}

func (a SecondaryAction) String() string {
	return fmt.Sprintf("Secondary(resource=%s, force=%t)", a.ResourceName, a.Force)
}

// ResizeAction resizes a DRBD device.
type ResizeAction struct {
	Minor     *uint
	SizeBytes int64
}

func (a ResizeAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return ConfiguredReasonError(
			fmt.Errorf("ResizeAction: minor not set"),
			v1alpha1.DRBDResourceCondConfiguredReasonResizeFailed,
		)
	}
	err := drbdsetup.ExecuteResize(ctx, *a.Minor, a.SizeBytes)
	return ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonResizeFailed)
}

func (a ResizeAction) String() string {
	minor := "<nil>"
	if a.Minor != nil {
		minor = fmt.Sprintf("%d", *a.Minor)
	}
	return fmt.Sprintf("Resize(minor=%s, sizeBytes=%d)", minor, a.SizeBytes)
}
