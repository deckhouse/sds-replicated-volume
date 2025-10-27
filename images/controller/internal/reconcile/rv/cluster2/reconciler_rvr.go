package cluster2

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type rvrReconciler struct {
	node NodeManager
	rv   RVAdapter
	rvr  RVRAdapter // optional

	dprops *replicaDynamicProps
}

type replicaDynamicProps struct {
	port   uint
	minor  uint
	nodeId uint
	disk   string
	peers  map[string]v1alpha2.Peer
}

func newRVRReconciler(
	node NodeManager,
	rv RVAdapter,
	rvr RVRAdapter, // optional
) (*rvrReconciler, error) {
	if node == nil {
		return nil, errArgNil("node")
	}
	if rv == nil {
		return nil, errArgNil("rv")
	}

	res := &rvrReconciler{
		node: node,
		rv:   rv,
		rvr:  rvr,
	}
	return res, nil
}

func (r *rvrReconciler) initializeDynamicProps(nodeIdMgr NodeIdManager) error {

	dprops := &replicaDynamicProps{}

	// port
	if r.rvr == nil || r.rvr.Port() == 0 {
		port, err := r.node.ReserveNodePort()
		if err != nil {
			return err
		}
		dprops.port = port
	} else {
		dprops.port = r.rvr.Port()
	}

	// minor
	if r.rvr == nil || r.rvr.Minor() == nil {
		minor, err := r.node.ReserveNodeMinor()
		if err != nil {
			return err
		}
		dprops.minor = minor
	} else {
		dprops.minor = *r.rvr.Minor()
	}

	// nodeid
	if r.rvr == nil {
		nodeId, err := nodeIdMgr.ReserveNodeId()
		if err != nil {
			return err
		}
		dprops.nodeId = nodeId
	} else {
		dprops.nodeId = r.rvr.Spec.NodeId
	}

	// disk
	// TODO
	// if !r.node.Diskless() {
	// 	if r.existingLLV == nil {
	// 		dprops.disk = fmt.Sprintf("/dev/%s/%s", r.node.LVGActualVGNameOnTheNode(), rvName)
	// 	} else {
	// 		dprops.disk = fmt.Sprintf("/dev/%s/%s", r.node.LVGActualVGNameOnTheNode(), r.existingLLV.Spec.ActualLVNameOnTheNode)
	// 	}
	// }

	r.dprops = dprops

	return nil
}

func (r *rvrReconciler) asPeer() v1alpha2.Peer {
	res := v1alpha2.Peer{
		NodeId: uint(r.dprops.nodeId),
		Address: v1alpha2.Address{
			IPv4: r.node.NodeIP(),
			Port: r.dprops.port,
		},
		Diskless:     r.node.Diskless(),
		SharedSecret: r.rv.SharedSecret(),
	}

	return res
}

func (r *rvrReconciler) initializePeers(allReplicas map[string]*rvrReconciler) error {
	peers := make(map[string]v1alpha2.Peer, len(allReplicas)-1)

	for _, repl := range allReplicas {
		if r == repl {
			continue
		}

		peers[repl.node.NodeName()] = repl.asPeer()
	}

	r.dprops.peers = peers

	return nil
}

func (r *rvrReconciler) createVolumeIfNeeded() (Action, error) {
	if r.node.Diskless() {
		return nil, nil
	}

	var res Actions
	// if r.existingLLV == nil {
	// 	// newLLV := &snc.LVMLogicalVolume{

	// 	// }
	// 	res = append(
	// 		res,
	// 		CreateLVMLogicalVolume{},
	// 		WaitLVMLogicalVolume{},
	// 	)
	// } else {

	// }

	return res, nil
}
