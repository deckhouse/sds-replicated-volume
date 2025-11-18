package cluster

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
)

// isDuplicateError checks if error is about duplicate minor/port
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "duplicate nodeMinor") || strings.Contains(errStr, "duplicate port")
}

type diskPath interface {
	diskPath() string
}

type rvrReconciler struct {
	RVNodeAdapter
	nodeMgr NodeManager

	existingRVR RVRAdapter // optional

	//
	rvrWriter             *RVRWriterImpl
	firstReplicaInCluster bool
	clusterHasRVRs        bool
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

	rvrBuilder, err := NewRVRWriterImpl(rvNode)
	if err != nil {
		return nil, err
	}

	res := &rvrReconciler{
		RVNodeAdapter: rvNode,
		nodeMgr:       nodeMgr,
		rvrWriter:     rvrBuilder,
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

	// Reserve minor and port from existing RVR to prevent conflicts
	// Note: minor/port might already be reserved during nodeManager initialization,
	// so we ignore duplicate errors
	if rvr.Minor() >= 0 {
		if err := rec.nodeMgr.ReserveNodeMinor(uint(rvr.Minor())); err != nil {
			// Ignore duplicate errors - minor might already be reserved during initialization
			if !isDuplicateError(err) {
				return err
			}
		}
	}
	if rvr.Port() > 0 {
		if err := rec.nodeMgr.ReserveNodePort(rvr.Port()); err != nil {
			// Ignore duplicate errors - port might already be reserved during initialization
			if !isDuplicateError(err) {
				return err
			}
		}
	}

	rec.existingRVR = rvr
	rec.clusterHasRVRs = true
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
		rec.rvrWriter.SetPort(port)
	} else {
		rec.rvrWriter.SetPort(rec.existingRVR.Port())
	}

	// nodeid
	if rec.existingRVR == nil {
		nodeId, err := nodeIdMgr.NewNodeId()
		if err != nil {
			return err
		}
		rec.rvrWriter.SetNodeId(nodeId)
		if nodeId == 0 {
			rec.firstReplicaInCluster = true
		}
	} else {
		rec.rvrWriter.SetNodeId(rec.existingRVR.NodeId())
	}

	// minor
	vol := v1alpha2.Volume{}
	if rec.existingRVR == nil || rec.existingRVR.Minor() < 0 {
		minor, err := rec.nodeMgr.NewNodeMinor()
		if err != nil {
			return err
		}
		vol.Device = minor
	} else {
		vol.Device = uint(rec.existingRVR.Minor())
	}

	// if diskful
	if dp != nil {
		// disk
		vol.Disk = dp.diskPath()

	}

	rec.rvrWriter.SetVolume(vol)

	return nil
}

func (rec *rvrReconciler) initializePeers(allReplicas map[string]*rvrReconciler) error {
	for _, peerRec := range allReplicas {
		if rec == peerRec {
			continue
		}

		if peerRec.clusterHasRVRs {
			rec.clusterHasRVRs = true
		}

		rec.rvrWriter.SetPeer(peerRec.NodeName(), peerRec.rvrWriter.ToPeer())
	}

	return nil
}

func (rec *rvrReconciler) reconcile() (Action, error) {
	var res Actions
	if rec.existingRVR == nil {
		res = append(
			res,
			CreateRVR{
				Writer:              rec.rvrWriter,
				InitialSyncRequired: !rec.clusterHasRVRs && rec.firstReplicaInCluster,
			},
		)
	} else {
		// TODO: handle error/recreate/replace scenarios
		res = append(
			res,
			PatchRVR{
				RVR:    rec.existingRVR,
				Writer: rec.rvrWriter,
			},
		)

		existingRVRSize := rec.existingRVR.Size()
		targetSize := rec.Size()

		if existingRVRSize < targetSize {
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
