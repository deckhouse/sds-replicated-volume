package cluster2

type llvReconciler struct {
	node RVNodeAdapter
	llv  LLVAdapter
}

func newLLVReconciler(node RVNodeAdapter) (*llvReconciler, error) {
	if node == nil {
		return nil, errArgNil("node")
	}
	res := &llvReconciler{
		node: node,
	}

	return res, nil
}

func (a *llvReconciler) setExistingLLV(llv LLVAdapter) error {
	if llv == nil {
		return errArgNil("llv")
	}

	if a.llv != nil {
		return errInvalidCluster(
			"expected single LLV on the node, got: %s, %s",
			a.llv.LLVName(), llv.LLVName(),
		)
	}

	if llv.LVGName() != a.node.LVGName() {
		return errInvalidCluster(
			"expected llv spec.lvmVolumeGroupName to be '%s', got '%s'",
			llv.LVGName(), a.node.LVGName(),
		)
	}

	a.llv = llv

	return nil
}
