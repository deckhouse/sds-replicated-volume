package cluster2

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type llvAdapter struct {
	llvName                  string
	llvActualLVNameOnTheNode string
	lvgName                  string
}

type LLVAdapter interface {
	LLVName() string
	LLVActualLVNameOnTheNode() string
	LVGName() string
}

var _ LLVAdapter = &llvAdapter{}

func NewLLVAdapter(llv *snc.LVMLogicalVolume) (*llvAdapter, error) {
	if llv == nil {
		return nil, errArgNil("llv")
	}
	llvA := &llvAdapter{
		llvName:                  llv.Name,
		lvgName:                  llv.Spec.LVMVolumeGroupName,
		llvActualLVNameOnTheNode: llv.Spec.ActualLVNameOnTheNode,
	}
	return llvA, nil
}

func (l *llvAdapter) LVGName() string {
	return l.lvgName
}

func (l *llvAdapter) LLVName() string {
	return l.llvName
}

func (l *llvAdapter) LLVActualLVNameOnTheNode() string {
	return l.llvActualLVNameOnTheNode
}
