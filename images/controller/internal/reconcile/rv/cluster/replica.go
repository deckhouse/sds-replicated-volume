package cluster

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const rvrFinalizerName = "sds-replicated-volume.deckhouse.io/controller"

type replica struct {
	ctx                     context.Context
	llvCl                   LLVClient
	rvrCl                   RVRClient
	cfg                     Config
	id                      int
	rvName                  string
	nodeName                string
	ipv4                    string
	primary                 bool
	quorum                  byte
	quorumMinimumRedundancy byte

	// Indexes are volume ids.
	volumes []*volume

	// only non-nil after successful [replica.InitializeSelf] or [replica.Reconcile]
	rvr *v1alpha2.ReplicatedVolumeReplica
}

func (r *replica) AddVolume(
	actualVgNameOnTheNode string,
	actualLvNameOnTheNode string,
) *volume {
	v := &volume{
		actualVGNameOnTheNode: actualVgNameOnTheNode,
		actualLVNameOnTheNode: actualLvNameOnTheNode,
	}
	r.volumes = append(r.volumes, v)
	return v
}

func (r *replica) Initialized() bool { return r.rvr != nil }

func (r *replica) ReplicatedVolumeReplica() *v1alpha2.ReplicatedVolumeReplica {
	if r.rvr == nil {
		panic("expected Spec to be called after InitializeSelf or Reconcile")
	}
	return r.rvr.DeepCopy()
}

func (r *replica) InitializeSelf() error {
	nodeReplicas, err := r.rvrCl.ByNodeName(r.ctx, r.nodeName)
	if err != nil {
		return err
	}

	usedPorts := map[uint]struct{}{}
	usedMinors := map[uint]struct{}{}
	for _, item := range nodeReplicas {
		usedPorts[item.Spec.NodeAddress.Port] = struct{}{}
		for _, v := range item.Spec.Volumes {
			usedMinors[v.Device] = struct{}{}
		}
	}

	portMin, portMax := r.cfg.DRBDPortMinMax()

	freePort, err := findLowestUnusedInRange(usedPorts, portMin, portMax)
	if err != nil {
		return fmt.Errorf("unable to find free port on node %s: %w", r.nodeName, err)
	}

	// volumes
	var volumes []v1alpha2.Volume
	for volId, vol := range r.volumes {
		freeMinor, err := findLowestUnusedInRange(usedMinors, 0, 1048576)
		if err != nil {
			return fmt.Errorf("unable to find free minor on node %s: %w", r.nodeName, err)
		}
		usedMinors[freeMinor] = struct{}{}

		volumes = append(
			volumes,
			v1alpha2.Volume{
				Number: uint(volId),
				Disk:   fmt.Sprintf("/dev/%s/%s", vol.actualVGNameOnTheNode, vol.actualLVNameOnTheNode),
				Device: freeMinor,
			},
		)
	}

	// initialize
	r.rvr = &v1alpha2.ReplicatedVolumeReplica{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", r.rvName),
			Finalizers:   []string{rvrFinalizerName},
		},
		Spec: v1alpha2.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: r.rvName,
			NodeName:             r.nodeName,
			NodeId:               uint(r.id),
			NodeAddress: v1alpha2.Address{
				IPv4: r.ipv4,
				Port: freePort,
			},
			Volumes:                 volumes,
			Primary:                 r.primary,
			Quorum:                  r.quorum,
			QuorumMinimumRedundancy: r.quorumMinimumRedundancy,
		},
	}

	return nil
}

func (r *replica) InitializePeers(initializedReplicas []*replica) error {
	if r.rvr == nil {
		panic("expected InitializePeers to be called after InitializeSelf")
	}

	// find any replica with shared secret initialized, or generate one
	var sharedSecret string
	for _, peer := range initializedReplicas {
		if peer == r {
			continue
		}
		peerRvr := peer.ReplicatedVolumeReplica()
		if peerRvr.Spec.SharedSecret != "" {
			sharedSecret = peerRvr.Spec.SharedSecret
		}
	}
	if sharedSecret == "" {
		sharedSecret = rand.Text()
	}
	r.rvr.Spec.SharedSecret = sharedSecret

	// peers
	for nodeId, peer := range initializedReplicas {
		if peer == r {
			continue
		}
		peerRvr := peer.ReplicatedVolumeReplica()

		if r.rvr.Spec.Peers == nil {
			r.rvr.Spec.Peers = map[string]v1alpha2.Peer{}
		}

		diskless, err := peerRvr.Diskless()
		if err != nil {
			return fmt.Errorf("determining disklessness for rvr %s: %w", peerRvr.Name, err)
		}

		r.rvr.Spec.Peers[peer.nodeName] = v1alpha2.Peer{
			NodeId:   uint(nodeId),
			Address:  peerRvr.Spec.NodeAddress,
			Diskless: diskless,
		}
	}

	return nil
}

func (r *replica) Reconcile(rvrs []*v1alpha2.ReplicatedVolumeReplica) (res []Action, err error) {
	// guaranteed to match replica:
	// - rvr.Spec.ReplicatedVolumeName
	// - rvr.Spec.NodeId,
	// everything else should be reconciled

	if rvrs[0].Spec.NodeName != r.nodeName {

	}

	// make sure SharedSecret is initialized
	return
}

func findLowestUnusedInRange(used map[uint]struct{}, minVal, maxVal uint) (uint, error) {
	for i := minVal; i <= maxVal; i++ {
		if _, ok := used[i]; !ok {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unable to find a free number in range [%d;%d]", minVal, maxVal)
}
