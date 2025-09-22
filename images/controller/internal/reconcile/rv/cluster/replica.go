package cluster

import (
	"context"
	"fmt"

	umaps "github.com/deckhouse/sds-common-lib/utils/maps"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
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

	// Indexes are volume ids.
	volumes []*volume

	// properties, which should be determined dynamically
	dprops *replicaDynamicProps
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

type replicaInitResult struct {
	ReplicatedVolumeReplica   *v1alpha2.ReplicatedVolumeReplica
	NewLVMLogicalVolumes      []snc.LVMLogicalVolume
	ExistingLVMLogicalVolumes []snc.LVMLogicalVolume
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

func (r *replica) Port() (uint, error) {
	if r.dprops != nil {
		return r.dprops.port, nil
	}

	freePort, err := r.portMgr.ReserveNodePort(r.ctx, r.props.nodeName)
	if err != nil {
		return 0, err
	}

	r.dprops = &replicaDynamicProps{
		port: freePort,
	}

	return freePort, nil
}

func (r *replica) Initialize(allReplicas []*replica) (Action, error) {
	var actions Actions

	// volumes
	rvrVolumes := make([]v1alpha2.Volume, len(r.volumes))
	for i, vol := range r.volumes {
		volAction, err := vol.Initialize(&rvrVolumes[i])
		if err != nil {
			return nil, err
		}

		actions = append(actions, volAction)
	}

	// initialize
	port, err := r.Port()
	if err != nil {
		return nil, err
	}

	var rvrPeers map[string]v1alpha2.Peer
	for nodeId, peer := range allReplicas {
		if peer == r {
			continue
		}

		diskless := len(peer.volumes) == 0

		port, err := peer.Port()
		if err != nil {
			return nil, err
		}

		rvrPeers = umaps.Set(
			rvrPeers,
			peer.props.nodeName,
			v1alpha2.Peer{
				NodeId: uint(nodeId),
				Address: v1alpha2.Address{
					IPv4: peer.props.ipv4,
					Port: port,
				},
				Diskless: diskless,
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
				Port: port,
			},
			SharedSecret:            r.props.sharedSecret,
			Volumes:                 rvrVolumes,
			Primary:                 r.props.primary,
			Quorum:                  r.props.quorum,
			QuorumMinimumRedundancy: r.props.quorumMinimumRedundancy,
		},
	}

	actions = append(
		actions,
		CreateReplicatedVolumeReplica{rvr},
		WaitReplicatedVolumeReplica{rvr},
	)

	return actions, nil
}

func setIfNeeded[T comparable](changeTracker *bool, current *T, expected T) {
	if *current == expected {
		return
	}
	*current = expected
	*changeTracker = true
}

// rvrs is non-empty slice of RVRs, which are guaranteed to match replica's:
//   - rvr.Spec.ReplicatedVolumeName
//   - rvr.Spec.NodeId
//   - rvr.Spec.NodeName
//
// Everything else should be reconciled.
func (r *replica) Reconcile(
	rvrs []*v1alpha2.ReplicatedVolumeReplica,
	peers []*replica,
) (Action, error) {

	var pa ParallelActions

	var invalid []*v1alpha2.ReplicatedVolumeReplica

	// reconcile every each
	for _, rvr := range rvrs {
		// if immutable props are invalid - rvr should be recreated
		// but creation & readiness should come before deletion

		if len(rvr.Spec.Volumes) != len(r.volumes) {
			invalid = append(invalid, rvr)
			continue
		}

		for id, vol := range r.volumes {
			rvrVol := &rvr.Spec.Volumes[id]
			if rvrVol.Number != uint(id) {
				invalid = append(invalid, rvr)
				continue
			}
			if rvrVol.Device != vol.dprops.minor {
				invalid = append(invalid, rvr)
				continue
			}

		}

		//
		changed := new(bool)

		setIfNeeded(changed, &rvr.Spec.NodeAddress.IPv4, r.props.ipv4)
		setIfNeeded(changed, &rvr.Spec.Primary, r.props.primary)
		setIfNeeded(changed, &rvr.Spec.Quorum, r.props.quorum)
		setIfNeeded(changed, &rvr.Spec.QuorumMinimumRedundancy, r.props.quorumMinimumRedundancy)
		setIfNeeded(changed, &rvr.Spec.SharedSecret, r.props.sharedSecret)

		// volumes

		//

		// peers

		//

		if *changed {
			// pa = append(
			// 	pa,
			// 	&ChangeReplicaSpec{rvr.DeepCopy()},
			// )
		}
	}

	// a = append(a, pa)

	// wait for any

	// delete the rest

	// make sure SharedSecret is initialized

	// TODO: intiialize dprops *replicaDynamicProps
	return nil, nil
}
