package cluster

import (
	"context"
	"fmt"
	"slices"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	uslices "github.com/deckhouse/sds-common-lib/utils/slices"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const rvrFinalizerName = "sds-replicated-volume.deckhouse.io/controller"

type Replica struct {
	ctx      context.Context
	llvCl    LLVClient
	rvrCl    RVRClient
	portMgr  PortManager
	minorMgr MinorManager
	props    replicaProps
	// properties, which should be determined dynamically
	dprops replicaDynamicProps

	// Indexes are volume ids.
	volumes []*Volume

	peers []*Replica
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
	existingRVR *v1alpha2.ReplicatedVolumeReplica
	port        uint
}

func (r *Replica) AddVolume(
	size int64,
	vgName string,
	actualVgNameOnTheNode string,
	llvProps LLVProps,
) *Volume {
	v := &Volume{
		ctx:      r.ctx,
		llvCl:    r.llvCl,
		rvrCl:    r.rvrCl,
		minorMgr: r.minorMgr,
		props: volumeProps{
			id:                    len(r.volumes),
			rvName:                r.props.rvName,
			nodeName:              r.props.nodeName,
			actualVGNameOnTheNode: actualVgNameOnTheNode,
			vgName:                vgName,
			size:                  size,
			llvProps:              llvProps,
		},
	}
	r.volumes = append(r.volumes, v)
	return v
}

func (r *Replica) diskless() bool {
	return len(r.volumes) == 0
}

func (r *Replica) initialize(
	existingRVR *v1alpha2.ReplicatedVolumeReplica,
	allReplicas []*Replica,
) error {
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

	for _, vol := range r.volumes {
		var existingRVRVolume *v1alpha2.Volume
		if existingRVR != nil {
			existingRVRVolume, _ = uiter.Find(
				uslices.Ptrs(existingRVR.Spec.Volumes),
				func(rvrVol *v1alpha2.Volume) bool {
					return rvrVol.Number == uint(vol.props.id)
				},
			)
		}

		err := vol.initialize(existingRVRVolume)
		if err != nil {
			return err
		}
	}

	r.dprops = replicaDynamicProps{
		port:        port,
		existingRVR: existingRVR,
	}

	r.peers = slices.Collect(
		uiter.Filter(
			slices.Values(allReplicas),
			func(peer *Replica) bool { return r != peer },
		),
	)
	return nil
}

func (r *Replica) rvr(recreatedFromName string) *v1alpha2.ReplicatedVolumeReplica {
	// volumes
	rvrVolumes := make([]v1alpha2.Volume, 0, len(r.volumes))
	for _, vol := range r.volumes {
		rvrVolumes = append(rvrVolumes, vol.rvrVolume())
	}

	// peers
	var rvrPeers map[string]v1alpha2.Peer
	for nodeId, peer := range r.peers {
		rvrPeers = umaps.Set(
			rvrPeers,
			peer.props.nodeName,
			v1alpha2.Peer{
				NodeId: uint(nodeId),
				Address: v1alpha2.Address{
					IPv4: peer.props.ipv4,
					Port: peer.dprops.port,
				},
				Diskless: peer.diskless(),
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
		if rvr.Annotations == nil {
			rvr.Annotations = map[string]string{}
		}
		// TODO: may be old rvr should be deleted by controller, not agent?
		rvr.Annotations[v1alpha2.AnnotationKeyRecreatedFrom] = recreatedFromName
	}
	return rvr
}

func (r *Replica) reconcileVolumes() Action {
	var actions Actions
	for _, vol := range r.volumes {
		a := vol.reconcile()
		if a != nil {
			actions = append(actions, a)
		}
	}
	if len(actions) == 0 {
		return nil
	}
	return actions
}

func (r *Replica) recreateOrFix() Action {
	// if immutable props are invalid - rvr should be recreated
	// but creation & readiness should come before deletion
	if r.shouldBeRecreated(r.dprops.existingRVR) {
		rvr := r.rvr(r.dprops.existingRVR.Name)
		return Actions{
			CreateReplicatedVolumeReplica{rvr},
			WaitReplicatedVolumeReplica{rvr},
		}
	} else if r.shouldBeFixed(r.dprops.existingRVR) {
		return Actions{
			RVRPatch{ReplicatedVolumeReplica: r.dprops.existingRVR, Apply: r.makeFix()},
			WaitReplicatedVolumeReplica{r.dprops.existingRVR},
		}
	}

	return nil
}

func (r *Replica) shouldBeRecreated(rvr *v1alpha2.ReplicatedVolumeReplica) bool {
	if len(rvr.Spec.Volumes) != len(r.volumes) {
		return true
	}

	for id, vol := range r.volumes {
		rvrVol := &rvr.Spec.Volumes[id]

		if vol.shouldBeRecreated(rvrVol) {
			return true
		}
	}

	for _, peer := range r.peers {
		rvrPeer, ok := rvr.Spec.Peers[peer.props.nodeName]
		if !ok {
			continue
		}

		if rvrPeer.NodeId != peer.props.id {
			return true
		}

		if rvrPeer.Diskless != peer.diskless() {
			return true
		}
	}

	return false
}

func (r *Replica) shouldBeFixed(rvr *v1alpha2.ReplicatedVolumeReplica) bool {
	if rvr.Spec.NodeAddress.IPv4 != r.props.ipv4 {
		return true
	}
	if rvr.Spec.Primary != r.props.primary {
		return true
	}
	if rvr.Spec.Quorum != r.props.quorum {
		return true
	}
	if rvr.Spec.QuorumMinimumRedundancy != r.props.quorumMinimumRedundancy {
		return true
	}
	if rvr.Spec.SharedSecret != r.props.sharedSecret {
		return true
	}
	if len(rvr.Spec.Peers) != len(r.peers) {
		return true
	}

	for _, peer := range r.peers {
		rvrPeer, ok := rvr.Spec.Peers[peer.props.nodeName]
		if !ok {
			return true
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

func (r *Replica) makeFix() func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
	return func(rvr *v1alpha2.ReplicatedVolumeReplica) error {
		if r.shouldBeRecreated(rvr) {
			return fmt.Errorf(
				"can not patch rvr %s, since it should be recreated",
				rvr.Name,
			)
		}

		if !r.shouldBeFixed(rvr) {
			return nil
		}

		rvr.Spec.NodeAddress.IPv4 = r.props.ipv4
		rvr.Spec.Primary = r.props.primary
		rvr.Spec.Quorum = r.props.quorum
		rvr.Spec.QuorumMinimumRedundancy = r.props.quorumMinimumRedundancy
		rvr.Spec.SharedSecret = r.props.sharedSecret

		// recreate peers
		rvr.Spec.Peers = map[string]v1alpha2.Peer{}
		for nodeId, peer := range r.peers {
			rvr.Spec.Peers[peer.props.nodeName] =
				v1alpha2.Peer{
					NodeId: uint(nodeId),
					Address: v1alpha2.Address{
						IPv4: peer.props.ipv4,
						Port: peer.dprops.port,
					},
					Diskless: peer.diskless(),
				}
		}

		return nil
	}
}
