package cluster2

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type diskPath interface {
	diskPath() string
}

type rvrReconciler struct {
	RVNodeAdapter
	rv      RVAdapter
	nodeMgr NodeManager

	rvr RVRAdapter // optional

	tgtProps *replicaTargetProps
}

type replicaTargetProps struct {
	port   uint
	minor  uint
	nodeId uint
	disk   string
	peers  map[string]v1alpha2.Peer
}

func newRVRReconciler(
	rv RVAdapter,
	rvNode RVNodeAdapter,
	nodeMgr NodeManager,
) (*rvrReconciler, error) {
	if rv == nil {
		return nil, errArgNil("rv")
	}
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}
	if nodeMgr == nil {
		return nil, errArgNil("nodeMgr")
	}

	res := &rvrReconciler{
		RVNodeAdapter: rvNode,
		rv:            rv,
		nodeMgr:       nodeMgr,
	}
	return res, nil
}

func (rec *rvrReconciler) hasExisting() bool {
	return rec.rvr != nil
}

func (rec *rvrReconciler) setExistingRVR(rvr RVRAdapter) error {
	if rvr == nil {
		return errArgNil("rvr")
	}

	if rvr.NodeName() != rec.NodeName() {
		return errInvalidCluster(
			"expected rvr '%s' to have node name '%s', got '%s'",
			rvr.Name(), rec.NodeName(), rvr.NodeName(),
		)
	}

	if rec.rvr != nil {
		return errInvalidCluster(
			"expected one RVR on the node, got: %s, %s",
			rec.rvr.Name(), rvr.Name(),
		)
	}

	rec.rvr = rvr
	return nil
}

func (rec *rvrReconciler) initializeTargetProps(
	nodeIdMgr NodeIdManager,
	dp diskPath,
) error {

	if rec.Diskless() != (dp == nil) {
		return errUnexpected("expected rec.Diskless() == (dp == nil)")
	}

	tgtProps := &replicaTargetProps{}

	// port
	if rec.rvr == nil || rec.rvr.Port() == 0 {
		port, err := rec.nodeMgr.NewNodePort()
		if err != nil {
			return err
		}
		tgtProps.port = port
	} else {
		tgtProps.port = rec.rvr.Port()
	}

	// minor
	if rec.rvr == nil || rec.rvr.Minor() == nil {
		minor, err := rec.nodeMgr.NewNodeMinor()
		if err != nil {
			return err
		}
		tgtProps.minor = minor
	} else {
		tgtProps.minor = *rec.rvr.Minor()
	}

	// nodeid
	if rec.rvr == nil {
		nodeId, err := nodeIdMgr.NewNodeId()
		if err != nil {
			return err
		}
		tgtProps.nodeId = nodeId
	} else {
		tgtProps.nodeId = rec.rvr.NodeId()
	}

	// disk
	if dp != nil {
		tgtProps.disk = dp.diskPath()
	}

	rec.tgtProps = tgtProps

	return nil
}

func (rec *rvrReconciler) asPeer() v1alpha2.Peer {
	res := v1alpha2.Peer{
		NodeId: uint(rec.tgtProps.nodeId),
		Address: v1alpha2.Address{
			IPv4: rec.NodeIP(),
			Port: rec.tgtProps.port,
		},
		Diskless:     rec.Diskless(),
		SharedSecret: rec.rv.SharedSecret(),
	}

	return res
}

func (rec *rvrReconciler) initializePeers(allReplicas map[string]*rvrReconciler) error {
	peers := make(map[string]v1alpha2.Peer, len(allReplicas)-1)

	for _, peerRec := range allReplicas {
		if rec == peerRec {
			continue
		}

		peers[peerRec.NodeName()] = peerRec.asPeer()
	}

	rec.tgtProps.peers = peers

	return nil
}

func (rec *rvrReconciler) reconcile() (Action, error) {
	var res Action
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
