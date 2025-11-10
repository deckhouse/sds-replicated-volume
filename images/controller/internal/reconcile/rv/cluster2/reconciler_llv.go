package cluster2

import "fmt"

type llvReconciler struct {
	RVNodeAdapter

	existingLLV LLVAdapter // may be nil

	llvBuilder *LLVBuilder
}

var _ diskPath = &llvReconciler{}

func newLLVReconciler(rvNode RVNodeAdapter) (*llvReconciler, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}
	res := &llvReconciler{
		RVNodeAdapter: rvNode,
	}

	return res, nil
}

func (rec *llvReconciler) hasExisting() bool {
	return rec.existingLLV != nil
}

func (rec *llvReconciler) setExistingLLV(llv LLVAdapter) error {
	if llv == nil {
		return errArgNil("llv")
	}

	if rec.existingLLV != nil {
		return errInvalidCluster(
			"expected single LLV on the node, got: %s, %s",
			rec.existingLLV.LLVName(), llv.LLVName(),
		)
	}

	if llv.LVGName() != rec.LVGName() {
		return errInvalidCluster(
			"expected llv spec.lvmVolumeGroupName to be '%s', got '%s'",
			llv.LVGName(), rec.LVGName(),
		)
	}

	rec.existingLLV = llv

	return nil
}

func (rec *llvReconciler) diskPath() string {
	return fmt.Sprintf("/dev/%s/%s", rec.LVGActualVGNameOnTheNode(), rec.actualLVNameOnTheNode())
}

func (rec *llvReconciler) initializeDynamicProps() error {
	rec.llvBuilder.SetActualLVNameOnTheNode(rec.actualLVNameOnTheNode())
	return nil
}

func (rec *llvReconciler) actualLVNameOnTheNode() string {
	if rec.existingLLV == nil {
		return rec.RVName()
	} else {
		return rec.existingLLV.LLVActualLVNameOnTheNode()
	}
}

func (rec *llvReconciler) reconcile() (Action, error) {
	var res Actions

	if rec.existingLLV == nil {
		res = append(
			res,
			CreateLLV{
				InitLLV: rec.llvBuilder.BuildInitializer(),
			},
		)
	} else {
		// TODO: handle error/recreate/replace scenarios
		res = append(
			res,
			PatchLLV{
				PatchLLV: rec.llvBuilder.BuildInitializer(),
			},
		)
	}

	return res, nil
}
