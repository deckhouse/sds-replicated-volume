package cluster2

import (
	"maps"

	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type RVRBuilder struct {
	RVNodeAdapter
	port   uint
	nodeId uint
	volume *v1alpha2.Volume
	peers  map[string]v1alpha2.Peer
}

func NewRVRBuilder(rvNode RVNodeAdapter) (*RVRBuilder, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}

	return &RVRBuilder{
		RVNodeAdapter: rvNode,
		peers:         make(map[string]v1alpha2.Peer, rvNode.Replicas()-1),
	}, nil
}

type RVRInitializer func(*v1alpha2.ReplicatedVolumeReplica) error

func (b *RVRBuilder) SetPort(port uint) {
	b.port = port
}

func (b *RVRBuilder) SetNodeId(nodeId uint) {
	b.nodeId = nodeId
}

func (b *RVRBuilder) SetVolume(volume v1alpha2.Volume) {
	b.volume = &volume
}

func (b *RVRBuilder) AddPeer(nodeName string, peer v1alpha2.Peer) {
	b.peers = umaps.Set(b.peers, nodeName, peer)
}

func (b *RVRBuilder) BuildPeer() v1alpha2.Peer {
	return v1alpha2.Peer{
		NodeId: uint(b.nodeId),
		Address: v1alpha2.Address{
			IPv4: b.NodeIP(),
			Port: b.port,
		},
		Diskless:     b.Diskless(),
		SharedSecret: b.SharedSecret(),
	}
}

func (b *RVRBuilder) BuildInitializer() RVRInitializer {
	return func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
		rvrSpec := &rvr.Spec

		rvrSpec.ReplicatedVolumeName = b.RVName()
		rvrSpec.NodeName = b.NodeName()
		rvrSpec.NodeId = b.nodeId

		rvrSpec.NodeAddress.IPv4 = b.NodeIP()
		rvrSpec.NodeAddress.Port = b.port

		rvrSpec.Peers = maps.Clone(b.peers)

		if b.volume != nil {
			rvrSpec.Volumes = []v1alpha2.Volume{*b.volume}
		} else {
			rvrSpec.Volumes = nil
		}

		rvrSpec.SharedSecret = b.SharedSecret()
		rvrSpec.Primary = b.Primary()
		rvrSpec.Quorum = b.Quorum()
		rvrSpec.QuorumMinimumRedundancy = b.QuorumMinimumRedundancy()
		rvrSpec.AllowTwoPrimaries = b.AllowTwoPrimaries()
		return nil
	}
}
