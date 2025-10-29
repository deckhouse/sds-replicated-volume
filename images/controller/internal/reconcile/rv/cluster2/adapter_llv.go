package cluster2

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type llvAdapter struct {
}

type LLVAdapter interface {
	LLVName() string
	LVGName() string
}

var _ LLVAdapter = &llvAdapter{}

func NewLLVAdapter(llv *snc.LVMLogicalVolume) *llvAdapter {
	llvA := &llvAdapter{}

	return llvA
}

// LVMVolumeGroupName implements LLVAdapter.
func (l *llvAdapter) LVGName() string {
	panic("unimplemented")
}

// LLVName implements LLVAdapter.
func (l *llvAdapter) LLVName() string {
	panic("unimplemented")
}
