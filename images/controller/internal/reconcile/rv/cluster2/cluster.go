package cluster2

import (
	"log/slog"

	cmaps "github.com/deckhouse/sds-replicated-volume/lib/go/common/maps"
)

type Cluster struct {
	log *slog.Logger
	rv  RVAdapter

	rvrsByNodeName map[string]*rvrReconciler
	llvsByLVGName  map[string]*llvReconciler
	nodeIdMgr      nodeIdManager

	rvrsToDelete []RVRAdapter
	llvsToDelete []LLVAdapter
}

func NewCluster(
	log *slog.Logger,
	rv RVAdapter,
	rvNodes []RVNodeAdapter,
	nodeMgrs []NodeManager,
) (*Cluster, error) {
	if log == nil {
		log = slog.Default()
	}
	if rv == nil {
		return nil, errArgNil("rv")
	}

	if len(rvNodes) != len(nodeMgrs) {
		return nil,
			errArg("expected len(rvNodes)==len(nodeMgrs), got %d!=%d",
				len(rvNodes), len(nodeMgrs),
			)
	}

	// init reconcilers
	rvrsByNodeName := make(map[string]*rvrReconciler, len(rvNodes))
	llvsByLVGName := make(map[string]*llvReconciler, len(rvNodes))
	for i, rvNode := range rvNodes {
		if rvNode == nil {
			return nil, errArg("expected rvNodes not to have nil elements, got nil at %d", i)
		}

		nodeMgr := nodeMgrs[i]
		if nodeMgr == nil {
			return nil, errArg("expected nodeMgrs not to have nil elements, got nil at %d", i)
		}

		if rvNode.NodeName() != nodeMgr.NodeName() {
			return nil,
				errArg(
					"expected rvNodes elements to have the same node names as  nodeMgrs elements, got '%s'!='%s' at %d",
					rvNode.NodeName(), nodeMgr.NodeName(), i,
				)
		}

		rvr, err := newRVRReconciler(rv, rvNode, nodeMgr)
		if err != nil {
			return nil, err
		}

		var added bool
		if rvrsByNodeName, added = cmaps.SetUnique(rvrsByNodeName, rvNode.NodeName(), rvr); !added {
			return nil, errInvalidCluster("duplicate node name: %s", rvNode.NodeName())
		}

		if !rvNode.Diskless() {
			llv, err := newLLVReconciler(rvNode)
			if err != nil {
				return nil, err
			}

			if llvsByLVGName, added = cmaps.SetUnique(llvsByLVGName, rvNode.LVGName(), llv); !added {
				return nil, errInvalidCluster("duplicate lvg name: %s", rvNode.LVGName())
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

func (c *Cluster) AddExistingRVR(rvr RVRAdapter) (err error) {
	if rvr == nil {
		return errArgNil("rvr")
	}

	nodeId := rvr.NodeId()

	if err = c.nodeIdMgr.ReserveNodeId(nodeId); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			c.nodeIdMgr.FreeNodeId(nodeId)
		}
	}()

	rvrRec, ok := c.rvrsByNodeName[rvr.NodeName()]
	if ok {
		if err = rvrRec.setExistingRVR(rvr); err != nil {
			return err
		}
	} else {
		c.rvrsToDelete = append(c.rvrsToDelete, rvr)
	}

	return nil
}

func (c *Cluster) AddExistingLLV(llv LLVAdapter) error {
	if llv == nil {
		return errArgNil("llv")
	}

	llvA, ok := c.llvsByLVGName[llv.LVGName()]
	if ok {
		if err := llvA.setExistingLLV(llv); err != nil {
			return err
		}
	} else {
		c.llvsToDelete = append(c.llvsToDelete, llv)
	}

	return nil
}

func (c *Cluster) Reconcile() (Action, error) {
	// INITIALIZE

	for _, rvrRec := range c.rvrsByNodeName {
		if err := rvrRec.initializeDynamicProps(&c.nodeIdMgr); err != nil {
			return nil, err
		}
	}

	return nil, nil

	// for _, repl := range c.replicasByNodeName {
	// 	if err := repl.initializePeers(c.replicasByNodeName); err != nil {
	// 		return nil, err
	// 	}
	// }

	// //

	// var res Actions
	// for {
	// 	for nodeName, repl := range c.replicasByNodeName {
	// 		_ = nodeName
	// 		_ = repl
	// 	}
	// }
	// return res, nil
}
