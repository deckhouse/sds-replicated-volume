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
	corev1 "k8s.io/api/core/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// PortAllocator is a function that allocates a port for a given IP address.
type PortAllocator func(ip string) uint

// IntendedDRBDState represents the intended DRBD state for a resource.
// It focuses only on DRBD-specific state, not K8S object state.
type IntendedDRBDState interface {
	IsZero() bool

	IsUpAndNotInCleanup() bool

	// ResourceName returns the DRBD resource name on the node.
	ResourceName() string

	// NodeID returns this node's ID for the resource.
	NodeID() uint8

	// Type returns the resource type (Diskful or Diskless).
	Type() v1alpha1.DRBDResourceType

	// BackingDisk returns the path to the backing device for diskful resources.
	// Returns empty string for diskless resources.
	BackingDisk() string

	// Quorum returns the quorum setting. 0 means off.
	Quorum() byte

	// QuorumMinimumRedundancy returns the quorum minimum redundancy setting. 0 means off.
	QuorumMinimumRedundancy() byte

	// AllowTwoPrimaries returns whether dual-primary mode is allowed.
	AllowTwoPrimaries() bool

	// Role returns the intended role (Primary or Secondary).
	Role() v1alpha1.DRBDRole

	// Size returns the intended size in bytes. 0 for diskless resources.
	Size() int64

	// Peers returns the list of intended peer configurations.
	Peers() []IntendedPeer

	// AutoPromote returns the intended auto-promote setting. Always false.
	AutoPromote() bool

	// OnNoQuorum returns the intended on-no-quorum action. Always "suspend-io".
	OnNoQuorum() string

	// OnNoDataAccessible returns the intended on-no-data-accessible action. Always "suspend-io".
	OnNoDataAccessible() string

	// OnSuspendedPrimaryOutdated returns the intended action. Always "force-secondary".
	OnSuspendedPrimaryOutdated() string

	// DiscardZeroesIfAligned returns the intended setting. Always false.
	DiscardZeroesIfAligned() bool

	// RsDiscardGranularity returns the intended rs-discard-granularity. Always 8192.
	RsDiscardGranularity() uint
}

// IntendedPeer represents the intended state of a DRBD peer connection.
type IntendedPeer interface {
	// Name returns the peer's node name.
	Name() string

	// NodeID returns the peer's node ID.
	NodeID() uint8

	// Protocol returns the replication protocol (A, B, or C).
	Protocol() v1alpha1.DRBDProtocol

	// SharedSecret returns the shared secret for authentication.
	SharedSecret() string

	// SharedSecretAlg returns the HMAC algorithm for shared secret (e.g., SHA256, SHA1).
	SharedSecretAlg() v1alpha1.SharedSecretAlg

	// AllowRemoteRead returns whether reading from this peer is allowed.
	AllowRemoteRead() bool

	// RRConflict returns the intended rr-conflict policy. Always "retry-connect".
	RRConflict() string

	// Paths returns the network paths to this peer.
	Paths() []IntendedPath
}

// IntendedPath represents an intended network path to a DRBD peer.
type IntendedPath interface {
	// SystemNetworkName returns the system network name for this path.
	SystemNetworkName() string

	// LocalIPv4 returns the local IP address.
	LocalIPv4() string

	// LocalPort returns the local port.
	LocalPort() uint

	// RemoteIPv4 returns the remote peer's IP address.
	RemoteIPv4() string

	// RemotePort returns the remote peer's port.
	RemotePort() uint
}

// intendedDRBDState implements IntendedDRBDState with pre-computed values.
type intendedDRBDState struct {
	isUpAndNotInCleanup     bool
	resourceName            string
	nodeID                  uint8
	resourceType            v1alpha1.DRBDResourceType
	backingDisk             string
	quorum                  byte
	quorumMinimumRedundancy byte
	allowTwoPrimaries       bool
	role                    v1alpha1.DRBDRole
	sizeBytes               int64
	peers                   []IntendedPeer
}

func (s *intendedDRBDState) IsZero() bool              { return s == nil }
func (s *intendedDRBDState) IsUpAndNotInCleanup() bool { return s.isUpAndNotInCleanup }
func (s *intendedDRBDState) ResourceName() string      { return s.resourceName }
func (s *intendedDRBDState) NodeID() uint8             { return s.nodeID }
func (s *intendedDRBDState) Type() v1alpha1.DRBDResourceType {
	return s.resourceType
}
func (s *intendedDRBDState) BackingDisk() string           { return s.backingDisk }
func (s *intendedDRBDState) Quorum() byte                  { return s.quorum }
func (s *intendedDRBDState) QuorumMinimumRedundancy() byte { return s.quorumMinimumRedundancy }
func (s *intendedDRBDState) AllowTwoPrimaries() bool       { return s.allowTwoPrimaries }
func (s *intendedDRBDState) Role() v1alpha1.DRBDRole       { return s.role }
func (s *intendedDRBDState) Size() int64                   { return s.sizeBytes }
func (s *intendedDRBDState) Peers() []IntendedPeer         { return s.peers }

// Hardcoded resource options defaults
func (s *intendedDRBDState) AutoPromote() bool                  { return false }
func (s *intendedDRBDState) OnNoQuorum() string                 { return "suspend-io" }
func (s *intendedDRBDState) OnNoDataAccessible() string         { return "suspend-io" }
func (s *intendedDRBDState) OnSuspendedPrimaryOutdated() string { return "force-secondary" }

// Hardcoded disk options defaults
func (s *intendedDRBDState) DiscardZeroesIfAligned() bool { return false }
func (s *intendedDRBDState) RsDiscardGranularity() uint   { return 8192 } // TODO: DETECT AUTOMATICALLY FROM LVM

var _ IntendedDRBDState = (*intendedDRBDState)(nil)

// intendedPeer implements IntendedPeer with pre-computed values.
type intendedPeer struct {
	name            string
	nodeID          uint8
	protocol        v1alpha1.DRBDProtocol
	sharedSecret    string
	sharedSecretAlg v1alpha1.SharedSecretAlg
	allowRemoteRead bool
	paths           []IntendedPath
}

func (p *intendedPeer) Name() string                              { return p.name }
func (p *intendedPeer) NodeID() uint8                             { return p.nodeID }
func (p *intendedPeer) Protocol() v1alpha1.DRBDProtocol           { return p.protocol }
func (p *intendedPeer) SharedSecret() string                      { return p.sharedSecret }
func (p *intendedPeer) SharedSecretAlg() v1alpha1.SharedSecretAlg { return p.sharedSecretAlg }
func (p *intendedPeer) AllowRemoteRead() bool                     { return p.allowRemoteRead }
func (p *intendedPeer) RRConflict() string                        { return "retry-connect" }
func (p *intendedPeer) Paths() []IntendedPath                     { return p.paths }

var _ IntendedPeer = (*intendedPeer)(nil)

// intendedPath implements IntendedPath with pre-computed values.
type intendedPath struct {
	systemNetworkName string
	localIPv4         string
	localPort         uint
	remoteIPv4        string
	remotePort        uint
}

func (p *intendedPath) SystemNetworkName() string { return p.systemNetworkName }
func (p *intendedPath) LocalIPv4() string         { return p.localIPv4 }
func (p *intendedPath) LocalPort() uint           { return p.localPort }
func (p *intendedPath) RemoteIPv4() string        { return p.remoteIPv4 }
func (p *intendedPath) RemotePort() uint          { return p.remotePort }

var _ IntendedPath = (*intendedPath)(nil)

// systemNetworkToNodeAddressType maps system network names to Kubernetes Node address types.
func systemNetworkToNodeAddressType(systemNetwork string) corev1.NodeAddressType {
	switch systemNetwork {
	case "Internal":
		return corev1.NodeInternalIP
	case "External":
		return corev1.NodeExternalIP
	default:
		return corev1.NodeAddressType(systemNetwork + "IP")
	}
}

// computeIntendedDRBDState constructs the intended DRBD state from the DRBDResource.
// It requires addresses to already be computed in drbdr.status.addresses.
// The backingDisk parameter should be pre-fetched by the caller for diskful resources.
func computeIntendedDRBDState(
	drbdr *v1alpha1.DRBDResource,
	backingDisk string,
) *intendedDRBDState {
	// Build local addresses map from status.addresses
	localAddresses := make(map[string]v1alpha1.DRBDAddress, len(drbdr.Status.Addresses))
	for _, addr := range drbdr.Status.Addresses {
		localAddresses[addr.SystemNetworkName] = addr.Address
	}

	// Build peers
	peers := make([]IntendedPeer, 0, len(drbdr.Spec.Peers))
	for i := range drbdr.Spec.Peers {
		peer := &drbdr.Spec.Peers[i]
		paths := make([]IntendedPath, 0, len(peer.Paths))
		for j := range peer.Paths {
			peerPath := &peer.Paths[j]
			localAddr := localAddresses[peerPath.SystemNetworkName]
			paths = append(paths, &intendedPath{
				systemNetworkName: peerPath.SystemNetworkName,
				localIPv4:         localAddr.IPv4,
				localPort:         localAddr.Port,
				remoteIPv4:        peerPath.Address.IPv4,
				remotePort:        peerPath.Address.Port,
			})
		}
		peers = append(peers, &intendedPeer{
			name:            peer.Name,
			nodeID:          peer.NodeID,
			protocol:        peer.Protocol,
			sharedSecret:    peer.SharedSecret,
			sharedSecretAlg: peer.SharedSecretAlg,
			allowRemoteRead: peer.AllowRemoteRead,
			paths:           paths,
		})
	}

	// Compute isUpAndNotInCleanup
	isUpAndNotInCleanup := true
	if drbdr.DeletionTimestamp != nil && !obju.HasFinalizersOtherThan(drbdr, v1alpha1.AgentFinalizer) {
		isUpAndNotInCleanup = false
	} else if drbdr.Spec.State == v1alpha1.DRBDResourceStateDown {
		isUpAndNotInCleanup = false
	}

	// Compute size in bytes for diskful resources
	var sizeBytes int64
	if drbdr.Spec.Size != nil {
		sizeBytes = drbdr.Spec.Size.Value()
	}

	return &intendedDRBDState{
		isUpAndNotInCleanup:     isUpAndNotInCleanup,
		resourceName:            DRBDResourceNameOnTheNode(drbdr),
		nodeID:                  drbdr.Spec.NodeID,
		resourceType:            drbdr.Spec.Type,
		backingDisk:             backingDisk,
		quorum:                  drbdr.Spec.Quorum,
		quorumMinimumRedundancy: drbdr.Spec.QuorumMinimumRedundancy,
		allowTwoPrimaries:       drbdr.Spec.AllowTwoPrimaries,
		role:                    drbdr.Spec.Role,
		sizeBytes:               sizeBytes,
		peers:                   peers,
	}
}
