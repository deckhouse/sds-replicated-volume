package cluster

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

		if rvNode.RVName() != rv.RVName() {
			return nil,
				errArg(
					"expected rvNodes elements to have the same names as rv, got '%s'!='%s' at %d",
					rvNode.RVName(), rv.RVName(), i,
				)
		}

		rvr, err := newRVRReconciler(rvNode, nodeMgr)
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

	llvRec, ok := c.llvsByLVGName[llv.LVGName()]
	if ok {
		if err := llvRec.setExistingLLV(llv); err != nil {
			return err
		}
	} else {
		c.llvsToDelete = append(c.llvsToDelete, llv)
	}

	return nil
}

func (c *Cluster) deleteLLV(llv LLVAdapter) Action {
	return DeleteLLV{llv}
}

func (c *Cluster) deleteRVR(rvr RVRAdapter) Action {
	return DeleteRVR{rvr}
}

func (c *Cluster) initializeReconcilers() error {
	// llvs dynamic props
	for _, llvRec := range c.llvsByLVGName {
		if err := llvRec.initializeDynamicProps(); err != nil {
			return err
		}
	}

	// rvrs may need to query for some props
	for _, rvrRec := range c.rvrsByNodeName {
		var dp diskPath
		if !rvrRec.Diskless() {
			dp = c.llvsByLVGName[rvrRec.LVGName()]
		}

		if err := rvrRec.initializeDynamicProps(&c.nodeIdMgr, dp); err != nil {
			return err
		}
	}

	// initialize information about each other
	for _, rvrRec := range c.rvrsByNodeName {
		if err := rvrRec.initializePeers(c.rvrsByNodeName); err != nil {
			return err
		}
	}

	return nil
}

func (c *Cluster) Reconcile() (Action, error) {
	// 1. INITIALIZE
	if err := c.initializeReconcilers(); err != nil {
		return nil, err
	}

	// common for existing LLVs and RVRs
	var existingResourcesActions ParallelActions

	// 2. RECONCILE LLVs
	var addWithDeleteLLVActions Actions
	var addOrDeleteLLVActions ParallelActions
	{
		llvsToDelete := c.llvsToDelete
		for _, llvRec := range c.llvsByLVGName {
			reconcileAction, err := llvRec.reconcile()
			if err != nil {
				return nil, err
			}

			if llvRec.hasExisting() {
				existingResourcesActions = append(existingResourcesActions, reconcileAction)
			} else if len(llvsToDelete) > 0 {
				addWithDeleteLLVActions = append(addWithDeleteLLVActions, reconcileAction)
				addWithDeleteLLVActions = append(addWithDeleteLLVActions, c.deleteLLV(llvsToDelete[0]))
				llvsToDelete = llvsToDelete[1:]
			} else {
				addOrDeleteLLVActions = append(addOrDeleteLLVActions, reconcileAction)
			}
		}
		for len(llvsToDelete) > 0 {
			addOrDeleteLLVActions = append(addOrDeleteLLVActions, c.deleteLLV(llvsToDelete[0]))
			llvsToDelete = llvsToDelete[1:]
		}
	}

	// 3. RECONCILE RVRs
	var addWithDeleteRVRActions Actions
	var addOrDeleteRVRActions ParallelActions
	{
		rvrsToDelete := c.rvrsToDelete
		for _, rvrRec := range c.rvrsByNodeName {
			reconcileAction, err := rvrRec.reconcile()
			if err != nil {
				return nil, err
			}

			if rvrRec.hasExisting() {
				existingResourcesActions = append(existingResourcesActions, reconcileAction)
			} else if len(rvrsToDelete) > 0 {
				addWithDeleteRVRActions = append(addWithDeleteRVRActions, reconcileAction)
				addWithDeleteRVRActions = append(addWithDeleteRVRActions, c.deleteRVR(rvrsToDelete[0]))
				rvrsToDelete = rvrsToDelete[1:]
			} else {
				addOrDeleteRVRActions = append(addOrDeleteRVRActions, reconcileAction)
			}
		}
		for len(rvrsToDelete) > 0 {
			addOrDeleteRVRActions = append(addOrDeleteRVRActions, c.deleteRVR(rvrsToDelete[0]))
			rvrsToDelete = rvrsToDelete[1:]
		}
	}

	// DONE
	result := Actions{
		existingResourcesActions,
		addWithDeleteLLVActions, addOrDeleteLLVActions,
		addWithDeleteRVRActions, addOrDeleteRVRActions,
	}

	return cleanActions(result), nil
}
