package cluster2

import (
	"context"
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	cmaps "github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

type NodeManager interface {
	NodeAdapter
	ReserveNodePort() (uint, error)
	ReserveNodeMinor() (uint, error)
}

type NodeIdManager interface {
	ReserveNodeId() (uint, error)
}

type Cluster struct {
	log *slog.Logger
	rv  RVAdapterWithOwned

	rvrsByNodeName map[string]*rvrReconciler
	llvsByLVGName  map[string]*llvReconciler

	rvrs []*v1alpha2.ReplicatedVolumeReplica

	rvrsToDelete []*v1alpha2.ReplicatedVolumeReplica
	llvsToDelete []*snc.LVMLogicalVolume
}

func NewCluster(
	log *slog.Logger,
	rv RVAdapterWithOwned,
	nodes []NodeManager,
) (*Cluster, error) {
	if log == nil {
		log = slog.Default()
	}
	if rv == nil {
		return nil, errArgNil("rv")
	}

	// init reconcilers
	rvrsByNodeName := make(map[string]*rvrReconciler, len(nodes))
	llvsByLVGName := make(map[string]*llvReconciler, len(nodes))
	for _, node := range nodes {
		rvr, err := newRVRReconciler(node, rv)
		if err != nil {
			return nil, err
		}

		var added bool
		if rvrsByNodeName, added = cmaps.SetUnique(rvrsByNodeName, node.NodeName(), rvr); !added {
			return nil, errInvalidCluster("duplicate node name: %s", node.NodeName())
		}

		if !node.Diskless() {
			llv, err := newLLVReconciler(node)
			if err != nil {
				return nil, err
			}

			if llvsByLVGName, added = cmaps.SetUnique(llvsByLVGName, node.LVGName(), llv); !added {
				return nil, errInvalidCluster("duplicate lvg name: %s", node.LVGName())
			}
		}
	}

	//
	c := &Cluster{
		log: log,
		rv:  rv,

		rvrsByNodeName: rvrsByNodeName,
		llvsByLVGName:  llvsByLVGName,
	}

	return c, nil
}

func (c *Cluster) Load() error {
	return nil
}

func (c *Cluster) AddExistingRVR(rvr *v1alpha2.ReplicatedVolumeReplica) error {
	if rvr == nil {
		return errArgNil("rvr")
	}

	rvrA, ok := c.rvrsByNodeName[rvr.Spec.NodeName]
	if ok {
		if err := rvrA.setExistingRVR(rvr); err != nil {
			return err
		}
	} else {
		c.rvrsToDelete = append(c.rvrsToDelete, rvr)
	}
	c.rvrs = append(c.rvrs, rvr)
	return nil
}

func (c *Cluster) AddExistingLLV(llv *snc.LVMLogicalVolume) error {
	if llv == nil {
		return errArgNil("llv")
	}

	llvA, ok := c.llvAdaptersByLVGName[llv.Spec.LVMVolumeGroupName]
	if ok {
		if err := llvA.setExistingLLV(llv); err != nil {
			return err
		}
	} else {
		c.llvsToDelete = append(c.llvsToDelete, llv)
	}

	return nil
}

func (c *Cluster) Reconcile(ctx context.Context) (Action, error) {
	// INITIALIZE

	nodeIdMgr, err := NewNodeIdManager(c.rvrs)
	if err != nil {
		return nil, err
	}

	for _, repl := range c.replicasByNodeName {
		if err := repl.initializeDynamicProps(ctx, c.rv.Name, c.nodeMgr, nodeIdMgr); err != nil {
			return nil, err
		}
	}

	for _, repl := range c.replicasByNodeName {
		if err := repl.initializePeers(c.replicasByNodeName); err != nil {
			return nil, err
		}
	}

	//

	var res Actions
	for {
		for nodeName, repl := range c.replicasByNodeName {
			_ = nodeName
			_ = repl
		}
	}
	return res, nil
}
