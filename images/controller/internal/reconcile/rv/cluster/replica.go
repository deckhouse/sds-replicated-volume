package cluster

import (
	"context"
	"fmt"

	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const rvrFinalizerName = "sds-replicated-volume.deckhouse.io/controller"

type replica struct {
	ctx      context.Context
	llvCl    LLVClient
	rvrCl    RVRClient
	portMgr  PortManager
	minorMgr MinorManager
	props    replicaProps
	// properties, which should be determined dynamically
	dprops replicaDynamicProps

	// Indexes are volume ids.
	volumes []*volume

	existingRVR *v1alpha2.ReplicatedVolumeReplica
}

type replicaProps struct {
	id                      uint
	rvName                  string
	nodeName                string
	sharedSecret            string
	ipv4                    string
	primary                 bool
	quorum                  byte
	quorumMinimumRedundancy byte
}

type replicaDynamicProps struct {
	port uint
}

func (r *replica) AddVolume(actualVgNameOnTheNode string) *volume {
	v := &volume{
		ctx:      r.ctx,
		llvCl:    r.llvCl,
		rvrCl:    r.rvrCl,
		minorMgr: r.minorMgr,
		props: volumeProps{
			id:                    len(r.volumes),
			rvName:                r.props.rvName,
			nodeName:              r.props.nodeName,
			actualVGNameOnTheNode: actualVgNameOnTheNode,
		},
	}
	r.volumes = append(r.volumes, v)
	return v
}

func (r *replica) Diskless() bool {
	return len(r.volumes) == 0
}

func (r *replica) Initialize(existingRVR *v1alpha2.ReplicatedVolumeReplica) error {
	var port uint
	if existingRVR == nil {
		freePort, err := r.portMgr.ReserveNodePort(r.ctx, r.props.nodeName)
		if err != nil {
			return err
		}
		port = freePort
	} else {
		port = existingRVR.Spec.NodeAddress.Port
	}

	r.dprops = replicaDynamicProps{
		port: port,
	}
	r.existingRVR = existingRVR
	return nil
}

func (r *replica) InitializeVolumes() error {
	for _, vol := range r.volumes {

	}
	return nil
}

func (r *replica) Create(allReplicas []*replica, recreatedFromName string) (Action, error) {
	var actions Actions

	// volumes
	rvrVolumes := make([]v1alpha2.Volume, len(r.volumes))
	for i, vol := range r.volumes {
		volAction, err := vol.Create(&rvrVolumes[i])
		if err != nil {
			return nil, err
		}

		actions = append(actions, volAction)
	}

	// peers
	var rvrPeers map[string]v1alpha2.Peer
	for nodeId, peer := range allReplicas {
		if peer == r {
			continue
		}

		rvrPeers = umaps.Set(
			rvrPeers,
			peer.props.nodeName,
			v1alpha2.Peer{
				NodeId: uint(nodeId),
				Address: v1alpha2.Address{
					IPv4: peer.props.ipv4,
					Port: peer.dprops.port,
				},
				Diskless: peer.Diskless(),
			},
		)
	}

	rvr := &v1alpha2.ReplicatedVolumeReplica{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", r.props.rvName),
			Finalizers:   []string{rvrFinalizerName},
		},
		Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: r.props.rvName,
			NodeName:             r.props.nodeName,
			NodeId:               uint(r.props.id),
			NodeAddress: v1alpha2.Address{
				IPv4: r.props.ipv4,
				Port: r.dprops.port,
			},
			SharedSecret:            r.props.sharedSecret,
			Volumes:                 rvrVolumes,
			Primary:                 r.props.primary,
			Quorum:                  r.props.quorum,
			QuorumMinimumRedundancy: r.props.quorumMinimumRedundancy,
		},
	}

	if recreatedFromName != "" {
		rvr.Annotations[v1alpha2.AnnotationKeyRecreatedFrom] = recreatedFromName
	}

	actions = append(
		actions,
		CreateReplicatedVolumeReplica{rvr},
		WaitReplicatedVolumeReplica{rvr},
	)

	return actions, nil
}

// rvrs is non-empty slice of RVRs, which are guaranteed to match replica's:
//   - rvr.Spec.ReplicatedVolumeName
//   - rvr.Spec.NodeId
//   - rvr.Spec.NodeName
//
// Everything else should be reconciled.
func (r *replica) Reconcile(peers []*replica) (Action, error) {
	// if immutable props are invalid - rvr should be recreated
	// but creation & readiness should come before deletion

	if r.ShouldBeRecreated(r.existingRVR, peers) {
		return r.Create(peers, r.existingRVR.Name)
	} else if r.ShouldBeFixed(r.existingRVR, peers) {
		return r.Fix(peers), nil
	}

	return nil, nil
}

func (r *replica) ShouldBeRecreated(
	rvr *v1alpha2.ReplicatedVolumeReplica,
	peers []*replica,
) bool {
	if len(rvr.Spec.Volumes) != len(r.volumes) {
		return true
	}

	for id, vol := range r.volumes {
		rvrVol := &rvr.Spec.Volumes[id]

		if vol.ShouldBeRecreated(rvrVol) {
			return true
		}
	}

	if len(rvr.Spec.Peers) != len(peers)-1 {
		return true
	}

	for _, peer := range peers {
		if peer == r {
			continue
		}

		rvrPeer, ok := rvr.Spec.Peers[peer.props.nodeName]
		if !ok {
			return true
		}

		if rvrPeer.NodeId != peer.props.id {
			return true
		}

		if rvrPeer.Diskless != peer.Diskless() {
			return true
		}
	}

	return false
}

func (r *replica) ShouldBeFixed(
	rvr *v1alpha2.ReplicatedVolumeReplica,
	peers []*replica,
) bool {
	if rvr.Spec.NodeAddress.IPv4 != r.props.ipv4 {
		return false
	}
	if rvr.Spec.Primary != r.props.primary {
		return false
	}
	if rvr.Spec.Quorum != r.props.quorum {
		return false
	}
	if rvr.Spec.QuorumMinimumRedundancy != r.props.quorumMinimumRedundancy {
		return false
	}
	if rvr.Spec.SharedSecret != r.props.sharedSecret {
		return false
	}

	for _, peer := range peers {
		if peer == r {
			continue
		}

		rvrPeer, ok := rvr.Spec.Peers[peer.props.nodeName]
		if !ok {
			// should never happen, since replica would require recreation, not fixing
			continue
		}

		if rvrPeer.Address.IPv4 != peer.props.ipv4 {
			return true
		}

		if rvrPeer.Address.Port != peer.dprops.port {
			return true
		}

		if rvrPeer.SharedSecret != peer.props.sharedSecret {
			return true
		}
	}

	return false
}

func (r *replica) Fix(peers []*replica) Action {
	patch := Patch[*v1alpha2.ReplicatedVolumeReplica](
		func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
			if r.ShouldBeRecreated(rvr, peers) {
				return fmt.Errorf(
					"can not patch rvr %s, since it should be recreated",
					rvr.Name,
				)
			}

			if !r.ShouldBeFixed(rvr, peers) {
				return nil
			}

			rvr.Spec.NodeAddress.IPv4 = r.props.ipv4
			rvr.Spec.Primary = r.props.primary
			rvr.Spec.Quorum = r.props.quorum
			rvr.Spec.QuorumMinimumRedundancy = r.props.quorumMinimumRedundancy
			rvr.Spec.SharedSecret = r.props.sharedSecret

			for _, peer := range peers {
				if peer == r {
					continue
				}

				rvrPeer := rvr.Spec.Peers[peer.props.nodeName]

				rvrPeer.Address.IPv4 = peer.props.ipv4
				rvrPeer.Address.Port = peer.dprops.port
			}

			return nil
		},
	)

	return Actions{patch, WaitReplicatedVolumeReplica{r.existingRVR}}
}
