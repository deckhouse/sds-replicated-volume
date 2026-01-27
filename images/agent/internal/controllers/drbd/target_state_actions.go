package drbd

import (
	"context"
	"fmt"
	"slices"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdmeta"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type Action interface {
	_action()
}

type PatchAction interface {
	Action
	ApplyPatch(drbdr *v1alpha1.DRBDResource) bool
}

type PatchStatusAction interface {
	Action
	ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) bool
}

type ExecuteDRBDAction interface {
	Action
	Execute(ctx context.Context) error
}

//

var _ PatchAction = AddAgentFinalizerAction{}
var _ PatchAction = RemoveAgentFinalizerAction{}
var _ PatchStatusAction = ConfigureIPAddressAction{}
var _ PatchStatusAction = AllocatePortsAction{}

var _ ExecuteDRBDAction = NewResourceAction{}
var _ ExecuteDRBDAction = ResourceOptionsAction{}
var _ ExecuteDRBDAction = NewMinorAction{}
var _ ExecuteDRBDAction = CreateMetadataAction{}
var _ ExecuteDRBDAction = AttachAction{}
var _ ExecuteDRBDAction = DiskOptionsAction{}
var _ ExecuteDRBDAction = NewPeerAction{}
var _ ExecuteDRBDAction = NetOptionsAction{}
var _ ExecuteDRBDAction = NewPathAction{}
var _ ExecuteDRBDAction = ConnectAction{}
var _ ExecuteDRBDAction = DisconnectAction{}
var _ ExecuteDRBDAction = DelPathAction{}
var _ ExecuteDRBDAction = DelPeerAction{}
var _ ExecuteDRBDAction = DownAction{}

//

func (AddAgentFinalizerAction) _action()    {}
func (RemoveAgentFinalizerAction) _action() {}
func (ConfigureIPAddressAction) _action()   {}
func (AllocatePortsAction) _action()        {}
func (NewResourceAction) _action()          {}
func (ResourceOptionsAction) _action()      {}
func (NewMinorAction) _action()             {}
func (CreateMetadataAction) _action()       {}
func (AttachAction) _action()               {}
func (DiskOptionsAction) _action()          {}
func (NewPeerAction) _action()              {}
func (NetOptionsAction) _action()           {}
func (NewPathAction) _action()              {}
func (ConnectAction) _action()              {}
func (DisconnectAction) _action()           {}
func (DelPathAction) _action()              {}
func (DelPeerAction) _action()              {}
func (DownAction) _action()                 {}

//

type AddAgentFinalizerAction struct{}

func (AddAgentFinalizerAction) ApplyPatch(drbdr *v1alpha1.DRBDResource) bool {
	return obju.AddFinalizer(drbdr, v1alpha1.AgentFinalizer)
}

//

type RemoveAgentFinalizerAction struct{}

func (RemoveAgentFinalizerAction) ApplyPatch(drbdr *v1alpha1.DRBDResource) bool {
	return obju.RemoveFinalizer(drbdr, v1alpha1.AgentFinalizer)
}

//

type ConfigureIPAddressAction struct {
	IPv4BySystemNetworkNames map[string]string
}

func (a ConfigureIPAddressAction) ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) (changed bool) {
	statusAddresses := drbdr.Status.Addresses

	// Track which original addresses were matched
	originalLen := len(statusAddresses)
	visitedIdxs := make([]bool, originalLen)

	// update and add new
	for snn, ipv4 := range a.IPv4BySystemNetworkNames {
		var found bool
		// Only iterate over original addresses (not newly added ones)
		for i := range originalLen {
			if visitedIdxs[i] {
				continue
			}
			if statusAddresses[i].SystemNetworkName != snn {
				continue
			}

			//
			found = true
			visitedIdxs[i] = true

			if statusAddresses[i].Address.IPv4 == ipv4 {
				// match
				break
			}

			// update
			changed = true
			statusAddresses[i].Address.IPv4 = ipv4
			break
		}

		if found {
			continue
		}

		// adding
		changed = true
		statusAddresses = append(
			statusAddresses,
			v1alpha1.DRBDResourceAddressStatus{
				SystemNetworkName: snn,
				Address: v1alpha1.DRBDAddress{
					IPv4: ipv4,
					// leave port blank, it's created by another action
				},
			},
		)
	}

	// remove extra (iterate in reverse to avoid index shifting issues)
	for i := originalLen - 1; i >= 0; i-- {
		if visitedIdxs[i] {
			continue
		}
		changed = true
		statusAddresses = slices.Delete(statusAddresses, i, i+1)
	}

	// assign result back
	drbdr.Status.Addresses = statusAddresses
	return
}

//

type AllocatePortsAction struct {
	PortAllocator func(ip string) uint
}

func (a AllocatePortsAction) ApplyStatusPatch(drbdr *v1alpha1.DRBDResource) (changed bool) {
	for _, addrStatus := range drbdr.Status.Addresses {
		if addrStatus.Address.Port != 0 {
			// port already allocated
			continue
		}
		port := a.PortAllocator(addrStatus.Address.IPv4)
		if port == 0 {
			// problem allocating port
			continue
		}
		addrStatus.Address.Port = port
		changed = true
	}
	return
}

//
// ExecuteDRBDAction implementations
//

// NewResourceAction creates a new DRBD resource.
type NewResourceAction struct {
	ResourceName string
	NodeID       uint
}

func (a NewResourceAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteNewResource(ctx, a.ResourceName, a.NodeID)
}

//

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
	return drbdsetup.ExecuteResourceOptions(ctx, a.ResourceName, drbdsetup.ResourceOptions{
		AutoPromote:                a.AutoPromote,
		OnNoQuorum:                 a.OnNoQuorum,
		OnNoDataAccessible:         a.OnNoDataAccessible,
		OnSuspendedPrimaryOutdated: a.OnSuspendedPrimaryOutdated,
		Quorum:                     a.Quorum,
		QuorumMinimumRedundancy:    a.QuorumMinimumRedundancy,
	})
}

//

// NewMinorAction creates a new DRBD device/volume within a resource.
type NewMinorAction struct {
	ResourceName   string
	Volume         uint
	AllocatedMinor *uint
}

func (a NewMinorAction) Execute(ctx context.Context) error {
	minor, err := drbdsetup.ExecuteNewAutoMinor(ctx, a.ResourceName, a.Volume)
	if err != nil {
		return err
	}
	if a.AllocatedMinor != nil {
		*a.AllocatedMinor = minor
	}
	return nil
}

//

// CreateMetadataAction creates DRBD metadata on a backing device.
type CreateMetadataAction struct {
	Minor      *uint
	BackingDev string
}

func (a CreateMetadataAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return fmt.Errorf("CreateMetadataAction: minor not set")
	}
	return drbdmeta.ExecuteCreateMD(ctx, *a.Minor, a.BackingDev)
}

//

// AttachAction attaches a backing device to a volume.
type AttachAction struct {
	Minor    *uint
	LowerDev string // Path to backing block device
	MetaDev  string // Path to meta-data device or "internal"
	MetaIdx  string // Meta-data index or "internal"/"flexible"
}

func (a AttachAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return fmt.Errorf("AttachAction: minor not set")
	}
	return drbdsetup.ExecuteAttach(ctx, *a.Minor, a.LowerDev, a.MetaDev, a.MetaIdx)
}

//

// DiskOptionsAction sets disk options on an attached volume.
type DiskOptionsAction struct {
	Minor                  *uint
	DiscardZeroesIfAligned *bool
	RsDiscardGranularity   *uint
}

func (a DiskOptionsAction) Execute(ctx context.Context) error {
	if a.Minor == nil {
		return fmt.Errorf("DiskOptionsAction: minor not set")
	}
	return drbdsetup.ExecuteDiskOptions(ctx, *a.Minor, drbdsetup.DiskOptions{
		DiscardZeroesIfAligned: a.DiscardZeroesIfAligned,
		RsDiscardGranularity:   a.RsDiscardGranularity,
	})
}

//

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
	return drbdsetup.ExecuteNewPeer(ctx, a.ResourceName, a.PeerNodeID, opts)
}

//

// NetOptionsAction sets network options on a peer connection.
type NetOptionsAction struct {
	ResourceName      string
	PeerNodeID        uint
	AllowTwoPrimaries *bool
	AllowRemoteRead   *bool
}

func (a NetOptionsAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteNetOptions(ctx, a.ResourceName, a.PeerNodeID, drbdsetup.NetOptions{
		AllowTwoPrimaries: a.AllowTwoPrimaries,
		AllowRemoteRead:   a.AllowRemoteRead,
	})
}

//

// NewPathAction adds a network path to a peer.
type NewPathAction struct {
	ResourceName string
	PeerNodeID   uint
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a NewPathAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteNewPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
}

//

// ConnectAction establishes connection to a peer.
type ConnectAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a ConnectAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteConnect(ctx, a.ResourceName, a.PeerNodeID)
}

//

// DisconnectAction disconnects from a peer.
type DisconnectAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a DisconnectAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteDisconnect(ctx, a.ResourceName, a.PeerNodeID)
}

//

// DelPathAction removes a network path from a peer.
type DelPathAction struct {
	ResourceName string
	PeerNodeID   uint
	LocalAddr    string // "ip:port" format
	RemoteAddr   string // "ip:port" format
}

func (a DelPathAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteDelPath(ctx, a.ResourceName, a.PeerNodeID, a.LocalAddr, a.RemoteAddr)
}

//

// DelPeerAction removes a peer connection.
type DelPeerAction struct {
	ResourceName string
	PeerNodeID   uint
}

func (a DelPeerAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteDelPeer(ctx, a.ResourceName, a.PeerNodeID)
}

//

// DownAction tears down a DRBD resource completely.
type DownAction struct {
	ResourceName string
}

func (a DownAction) Execute(ctx context.Context) error {
	return drbdsetup.ExecuteDown(ctx, a.ResourceName)
}
