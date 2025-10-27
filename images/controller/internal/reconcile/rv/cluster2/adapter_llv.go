package cluster2

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type llvAdapter struct {
}

type LLVAdapter interface {
}

var _ LLVAdapter = &llvAdapter{}

func NewLLVAdapter(llv *snc.LVMLogicalVolume) *llvAdapter {
	llvA := &llvAdapter{}

	return llvA
}
