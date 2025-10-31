package cluster2

import "fmt"

type llvReconciler struct {
	RVNodeAdapter
	rv RVAdapter

	llv LLVAdapter // may be nil
}

func (rec *llvReconciler) diskPath() string {
	var volName string
	if rec.llv == nil {
		volName = rec.rv.RVName()
	} else {
		volName = rec.llv.LLVActualLVNameOnTheNode()
	}

	return fmt.Sprintf("/dev/%s/%s", rec.LVGActualVGNameOnTheNode(), volName)
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
	return rec.llv != nil
}

func (rec *llvReconciler) setExistingLLV(llv LLVAdapter) error {
	if llv == nil {
		return errArgNil("llv")
	}

	if rec.llv != nil {
		return errInvalidCluster(
			"expected single LLV on the node, got: %s, %s",
			rec.llv.LLVName(), llv.LLVName(),
		)
	}

	if llv.LVGName() != rec.LVGName() {
		return errInvalidCluster(
			"expected llv spec.lvmVolumeGroupName to be '%s', got '%s'",
			llv.LVGName(), rec.LVGName(),
		)
	}

	rec.llv = llv

	return nil
}

// resizeNeeded - if size of any
func (rec *llvReconciler) reconcile() (a Action, err error) {
	return nil, nil
}
