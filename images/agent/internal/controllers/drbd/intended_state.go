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
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type IntendedState interface {
	IsZero() bool

	IsUpAndNotInCleanup() bool

	IPv4BySystemNetworkNames() map[string]string

	// ResourceName returns the DRBD resource name on the node.
	ResourceName() string

	// NodeID returns this node's ID for the resource.
	NodeID() uint

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

	// Peers returns the list of intended peer configurations.
	Peers() []IntendedPeer
}

type IntendedPeer interface {
	// Name returns the peer's node name.
	Name() string

	// NodeID returns the peer's node ID.
	NodeID() uint

	// Protocol returns the replication protocol (A, B, or C).
	Protocol() v1alpha1.DRBDProtocol

	// SharedSecret returns the shared secret for authentication.
	SharedSecret() string

	// SharedSecretAlg returns the HMAC algorithm for shared secret (e.g., SHA256, SHA1).
	SharedSecretAlg() v1alpha1.SharedSecretAlg

	// AllowRemoteRead returns whether reading from this peer is allowed.
	AllowRemoteRead() bool

	// Paths returns the network paths to this peer.
	Paths() []IntendedPath
}

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

// intendedState implements IntendedState.
type intendedState struct {
	drbdr *v1alpha1.DRBDResource

	// backingDisk is populated externally from LLV/LVG lookup.
	backingDisk string

	// localAddresses maps system network name to local address.
	localAddresses map[string]v1alpha1.DRBDAddress
}

func (iState *intendedState) IsZero() bool {
	return iState == nil
}

func (iState *intendedState) IsUpAndNotInCleanup() bool {
	if iState.drbdr.DeletionTimestamp != nil &&
		!obju.HasFinalizersOtherThan(iState.drbdr, v1alpha1.AgentFinalizer) {
		// it's time to cleanup, so ignoring an spec.state
		return false
	}

	return iState.drbdr.Spec.State != v1alpha1.DRBDResourceStateDown
}

func (iState *intendedState) IPv4BySystemNetworkNames() map[string]string {
	result := make(map[string]string, len(iState.localAddresses))
	for snn, addr := range iState.localAddresses {
		result[snn] = addr.IPv4
	}
	return result
}

func (iState *intendedState) ResourceName() string {
	return iState.drbdr.DRBDResourceNameOnTheNode()
}

func (iState *intendedState) NodeID() uint {
	return iState.drbdr.Spec.NodeID
}

func (iState *intendedState) Type() v1alpha1.DRBDResourceType {
	return iState.drbdr.Spec.Type
}

func (iState *intendedState) BackingDisk() string {
	return iState.backingDisk
}

func (iState *intendedState) Quorum() byte {
	return iState.drbdr.Spec.Quorum
}

func (iState *intendedState) QuorumMinimumRedundancy() byte {
	return iState.drbdr.Spec.QuorumMinimumRedundancy
}

func (iState *intendedState) AllowTwoPrimaries() bool {
	return iState.drbdr.Spec.AllowTwoPrimaries
}

func (iState *intendedState) Peers() []IntendedPeer {
	peers := make([]IntendedPeer, 0, len(iState.drbdr.Spec.Peers))
	for i := range iState.drbdr.Spec.Peers {
		peers = append(peers, &intendedPeer{
			peer:           &iState.drbdr.Spec.Peers[i],
			localAddresses: iState.localAddresses,
		})
	}
	return peers
}

var _ IntendedState = (*intendedState)(nil)

// intendedPeer implements IntendedPeer.
type intendedPeer struct {
	peer           *v1alpha1.DRBDResourcePeer
	localAddresses map[string]v1alpha1.DRBDAddress
}

func (p *intendedPeer) Name() string {
	return p.peer.Name
}

func (p *intendedPeer) NodeID() uint {
	return p.peer.NodeID
}

func (p *intendedPeer) Protocol() v1alpha1.DRBDProtocol {
	return p.peer.Protocol
}

func (p *intendedPeer) SharedSecret() string {
	return p.peer.SharedSecret
}

func (p *intendedPeer) SharedSecretAlg() v1alpha1.SharedSecretAlg {
	return p.peer.SharedSecretAlg
}

func (p *intendedPeer) AllowRemoteRead() bool {
	return p.peer.AllowRemoteRead
}

func (p *intendedPeer) Paths() []IntendedPath {
	paths := make([]IntendedPath, 0, len(p.peer.Paths))
	for i := range p.peer.Paths {
		peerPath := &p.peer.Paths[i]
		localAddr := p.localAddresses[peerPath.SystemNetworkName]
		paths = append(paths, &intendedPath{
			systemNetworkName: peerPath.SystemNetworkName,
			localIPv4:         localAddr.IPv4,
			localPort:         localAddr.Port,
			remoteIPv4:        peerPath.Address.IPv4,
			remotePort:        peerPath.Address.Port,
		})
	}
	return paths
}

var _ IntendedPeer = (*intendedPeer)(nil)

// intendedPath implements IntendedPath.
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

// IntendedStateParams contains parameters for constructing IntendedState.
type IntendedStateParams struct {
	// BackingDisk is the path to the backing device (from LLV/LVG lookup).
	BackingDisk string

	// LocalAddresses maps system network name to local address.
	// This is typically populated from DRBDResource.Status.Addresses.
	LocalAddresses map[string]v1alpha1.DRBDAddress
}

//nolint:unparam // error is kept for future validation extensibility
func getIntendedState(drbdr *v1alpha1.DRBDResource, params IntendedStateParams) (*intendedState, error) {
	return &intendedState{
		drbdr:          drbdr,
		backingDisk:    params.BackingDisk,
		localAddresses: params.LocalAddresses,
	}, nil
}
