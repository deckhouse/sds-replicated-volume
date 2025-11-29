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

package cluster

import (
	"maps"

	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2old"
)

type RVRWriterImpl struct {
	RVNodeAdapter
	port   uint
	nodeID uint
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

func (w *RVRWriterImpl) SetNodeID(nodeID uint) {
	w.nodeID = nodeID
}

func (w *RVRWriterImpl) SetVolume(volume v1alpha2.Volume) {
	w.volume = &volume
}

func (w *RVRWriterImpl) SetPeer(nodeName string, peer v1alpha2.Peer) {
	w.peers[nodeName] = peer
}

func (w *RVRWriterImpl) ToPeer() v1alpha2.Peer {
	return v1alpha2.Peer{
		NodeId: uint(w.nodeID),
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
	cs = Change(cs, "nodeId", &rvrSpec.NodeId, w.nodeID)
	cs = Change(cs, "nodeAddress.ipv4", &rvrSpec.NodeAddress.IPv4, w.NodeIP())
	cs = Change(cs, "nodeAddress.port", &rvrSpec.NodeAddress.Port, w.port)

	var peers map[string]v1alpha2.Peer
	if len(w.peers) > 0 {
		peers = maps.Clone(w.peers)
	}
	cs = ChangeDeepEqual(cs, "peers", &rvrSpec.Peers, peers)

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
