package cluster

import (
	"maps"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type RVRWriterImpl struct {
	RVNodeAdapter
	port   uint
	nodeId uint
	volume *v1alpha2.Volume
	peers  map[string]v1alpha2.Peer
}

var _ RVRWriter = &RVRWriterImpl{}

func NewRVRWriterImpl(rvNode RVNodeAdapter) (*RVRWriterImpl, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}

	return &RVRWriterImpl{
		RVNodeAdapter: rvNode,
		peers:         make(map[string]v1alpha2.Peer, rvNode.Replicas()-1),
	}, nil
}

type RVRInitializer func(*v1alpha2.ReplicatedVolumeReplica) error

func (w *RVRWriterImpl) SetPort(port uint) {
	w.port = port
}

func (w *RVRWriterImpl) SetNodeId(nodeId uint) {
	w.nodeId = nodeId
}

func (w *RVRWriterImpl) SetVolume(volume v1alpha2.Volume) {
	w.volume = &volume
}

func (w *RVRWriterImpl) SetPeer(nodeName string, peer v1alpha2.Peer) {
	w.peers[nodeName] = peer
}

func (w *RVRWriterImpl) ToPeer() v1alpha2.Peer {
	return v1alpha2.Peer{
		NodeId: uint(w.nodeId),
		Address: v1alpha2.Address{
			IPv4: w.NodeIP(),
			Port: w.port,
		},
		Diskless:     w.Diskless(),
		SharedSecret: w.SharedSecret(),
	}
}

func (w *RVRWriterImpl) WriteToRVR(rvr *v1alpha2.ReplicatedVolumeReplica) (ChangeSet, error) {
	rvrSpec := &rvr.Spec

	cs := ChangeSet{}

	cs = Change(cs, "replicatedVolumeName", &rvrSpec.ReplicatedVolumeName, w.RVName())
	cs = Change(cs, "nodeName", &rvrSpec.NodeName, w.NodeName())
	cs = Change(cs, "nodeId", &rvrSpec.NodeId, w.nodeId)
	cs = Change(cs, "nodeAddress.ipv4", &rvrSpec.NodeAddress.IPv4, w.NodeIP())
	cs = Change(cs, "nodeAddress.port", &rvrSpec.NodeAddress.Port, w.port)

	cs = ChangeDeepEqual(cs, "peers", &rvrSpec.Peers, maps.Clone(w.peers))

	var volumes []v1alpha2.Volume
	if w.volume != nil {
		volumes = []v1alpha2.Volume{*w.volume}
	}
	cs = ChangeDeepEqual(cs, "volumes", &rvrSpec.Volumes, volumes)

	cs = Change(cs, "sharedSecret", &rvrSpec.SharedSecret, w.SharedSecret())
	cs = Change(cs, "primary", &rvrSpec.Primary, w.Primary())
	cs = Change(cs, "quorum", &rvrSpec.Quorum, w.Quorum())
	cs = Change(cs, "quorumMinimumRedundancy", &rvrSpec.QuorumMinimumRedundancy, w.QuorumMinimumRedundancy())
	cs = Change(cs, "allowTwoPrimaries", &rvrSpec.AllowTwoPrimaries, w.AllowTwoPrimaries())

	return cs, nil
}
