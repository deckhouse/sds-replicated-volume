package cluster2

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type llvReconciler struct {
	node NodeAdapter
	llv  *snc.LVMLogicalVolume
}

func newLLVReconciler(node NodeAdapter) (*llvReconciler, error) {
	if node == nil {
		return nil, errArgNil("node")
	}
	res := &llvReconciler{
		node: node,
	}

	return res, nil
}

func (a *llvReconciler) setExistingLLV(llv *snc.LVMLogicalVolume) error {
	if llv == nil {
		return errArgNil("llv")
	}

	if a.llv != nil {
		return errInvalidCluster(
			"expected single LLV on the node, got: %s, %s",
			a.llv.Name, llv.Name,
		)
	}

	if llv.Spec.LVMVolumeGroupName != a.node.LVGName() {
		return errInvalidCluster(
			"expected llv spec.lvmVolumeGroupName to be '%s', got '%s'",
			llv.Spec.LVMVolumeGroupName, a.node.LVGName(),
		)
	}

	a.llv = llv

	return nil
}
