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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
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

// IntendedPeer represents the intended state of a DRBD peer connection.
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
	nodeID                  uint
	resourceType            v1alpha1.DRBDResourceType
	backingDisk             string
	quorum                  byte
	quorumMinimumRedundancy byte
	allowTwoPrimaries       bool
	peers                   []IntendedPeer
}

func (s *intendedDRBDState) IsZero() bool              { return s == nil }
func (s *intendedDRBDState) IsUpAndNotInCleanup() bool { return s.isUpAndNotInCleanup }
func (s *intendedDRBDState) ResourceName() string      { return s.resourceName }
func (s *intendedDRBDState) NodeID() uint              { return s.nodeID }
func (s *intendedDRBDState) Type() v1alpha1.DRBDResourceType {
	return s.resourceType
}
func (s *intendedDRBDState) BackingDisk() string           { return s.backingDisk }
func (s *intendedDRBDState) Quorum() byte                  { return s.quorum }
func (s *intendedDRBDState) QuorumMinimumRedundancy() byte { return s.quorumMinimumRedundancy }
func (s *intendedDRBDState) AllowTwoPrimaries() bool       { return s.allowTwoPrimaries }
func (s *intendedDRBDState) Peers() []IntendedPeer         { return s.peers }

var _ IntendedDRBDState = (*intendedDRBDState)(nil)

// intendedPeer implements IntendedPeer with pre-computed values.
type intendedPeer struct {
	name            string
	nodeID          uint
	protocol        v1alpha1.DRBDProtocol
	sharedSecret    string
	sharedSecretAlg v1alpha1.SharedSecretAlg
	allowRemoteRead bool
	paths           []IntendedPath
}

func (p *intendedPeer) Name() string                              { return p.name }
func (p *intendedPeer) NodeID() uint                              { return p.nodeID }
func (p *intendedPeer) Protocol() v1alpha1.DRBDProtocol           { return p.protocol }
func (p *intendedPeer) SharedSecret() string                      { return p.sharedSecret }
func (p *intendedPeer) SharedSecretAlg() v1alpha1.SharedSecretAlg { return p.sharedSecretAlg }
func (p *intendedPeer) AllowRemoteRead() bool                     { return p.allowRemoteRead }
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
// The backingDisk lookup requires Kubernetes API access.
func computeIntendedDRBDState(
	ctx context.Context,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
) (*intendedDRBDState, error) {
	// Build local addresses map from status.addresses
	localAddresses := make(map[string]v1alpha1.DRBDAddress, len(drbdr.Status.Addresses))
	for _, addr := range drbdr.Status.Addresses {
		localAddresses[addr.SystemNetworkName] = addr.Address
	}

	// Get backing disk path for diskful resources
	var backingDisk string
	if drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful && drbdr.Spec.LVMLogicalVolumeName != "" {
		var err error
		backingDisk, err = getBackingDiskPath(ctx, cl, drbdr.Spec.LVMLogicalVolumeName)
		if err != nil {
			return nil, fmt.Errorf("getting backing disk path: %w", err)
		}
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

	return &intendedDRBDState{
		isUpAndNotInCleanup:     isUpAndNotInCleanup,
		resourceName:            drbdr.DRBDResourceNameOnTheNode(),
		nodeID:                  drbdr.Spec.NodeID,
		resourceType:            drbdr.Spec.Type,
		backingDisk:             backingDisk,
		quorum:                  drbdr.Spec.Quorum,
		quorumMinimumRedundancy: drbdr.Spec.QuorumMinimumRedundancy,
		allowTwoPrimaries:       drbdr.Spec.AllowTwoPrimaries,
		peers:                   peers,
	}, nil
}

// getBackingDiskPath looks up LVMLogicalVolume and LVMVolumeGroup to construct
// the backing disk path in the format /dev/<vg>/<lv>.
func getBackingDiskPath(ctx context.Context, cl client.Client, llvName string) (string, error) {
	llv := &snc.LVMLogicalVolume{}
	if err := cl.Get(ctx, client.ObjectKey{Name: llvName}, llv); err != nil {
		return "", fmt.Errorf("getting LVMLogicalVolume %q: %w", llvName, err)
	}

	lvg := &snc.LVMVolumeGroup{}
	if err := cl.Get(ctx, client.ObjectKey{Name: llv.Spec.LVMVolumeGroupName}, lvg); err != nil {
		return "", fmt.Errorf("getting LVMVolumeGroup %q: %w", llv.Spec.LVMVolumeGroupName, err)
	}

	return v1alpha1.SprintDRBDDisk(lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode), nil
}
