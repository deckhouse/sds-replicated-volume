package cluster

import (
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

type diskPath interface {
	diskPath() string
}

type rvrReconciler struct {
	RVNodeAdapter
	nodeMgr NodeManager

	existingRVR RVRAdapter // optional

	//
	rvrBuilder *RVRBuilder
}

func newRVRReconciler(
	rvNode RVNodeAdapter,
	nodeMgr NodeManager,
) (*rvrReconciler, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}
	if nodeMgr == nil {
		return nil, errArgNil("nodeMgr")
	}

	rvrBuilder, err := NewRVRBuilder(rvNode)
	if err != nil {
		return nil, err
	}

	res := &rvrReconciler{
		RVNodeAdapter: rvNode,
		nodeMgr:       nodeMgr,
		rvrBuilder:    rvrBuilder,
	}
	return res, nil
}

func (rec *rvrReconciler) hasExisting() bool {
	return rec.existingRVR != nil
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

	if rec.existingRVR != nil {
		return errInvalidCluster(
			"expected one RVR on the node, got: %s, %s",
			rec.existingRVR.Name(), rvr.Name(),
		)
	}

	rec.existingRVR = rvr
	return nil
}

func (rec *rvrReconciler) initializeDynamicProps(
	nodeIdMgr NodeIdManager,
	dp diskPath,
) error {
	if rec.Diskless() != (dp == nil) {
		return errUnexpected("expected rec.Diskless() == (dp == nil)")
	}

	// port
	if rec.existingRVR == nil || rec.existingRVR.Port() == 0 {
		port, err := rec.nodeMgr.NewNodePort()
		if err != nil {
			return err
		}
		rec.rvrBuilder.SetPort(port)
	} else {
		rec.rvrBuilder.SetPort(rec.existingRVR.Port())
	}

	// nodeid
	if rec.existingRVR == nil {
		nodeId, err := nodeIdMgr.NewNodeId()
		if err != nil {
			return err
		}
		rec.rvrBuilder.SetNodeId(nodeId)
	} else {
		rec.rvrBuilder.SetNodeId(rec.existingRVR.NodeId())
	}

	// if diskful
	if dp != nil {
		vol := v1alpha2.Volume{}

		// disk
		vol.Disk = dp.diskPath()

		// minor
		if rec.existingRVR == nil || rec.existingRVR.Minor() < 0 {
			minor, err := rec.nodeMgr.NewNodeMinor()
			if err != nil {
				return err
			}
			vol.Device = minor
		} else {
			vol.Device = uint(rec.existingRVR.Minor())
		}

		rec.rvrBuilder.SetVolume(vol)
	}

	return nil
}

func (rec *rvrReconciler) initializePeers(allReplicas map[string]*rvrReconciler) error {
	for _, peerRec := range allReplicas {
		if rec == peerRec {
			continue
		}

		rec.rvrBuilder.AddPeer(peerRec.NodeName(), peerRec.rvrBuilder.BuildPeer())
	}

	return nil
}

func (rec *rvrReconciler) reconcile() (Action, error) {
	var res Actions
	if rec.existingRVR == nil {
		res = append(
			res,
			CreateRVR{
				InitRVR: rec.rvrBuilder.BuildInitializer(),
			},
		)
	} else {
		// TODO: handle error/recreate/replace scenarios
		res = append(
			res,
			PatchRVR{
				RVR:      rec.existingRVR,
				PatchRVR: rec.rvrBuilder.BuildInitializer(),
			},
		)

		if rec.existingRVR.Size() != rec.Size() {
			res = append(
				res,
				ResizeRVR{
					RVR: rec.existingRVR,
				},
			)
		}
	}
	return res, nil
}
