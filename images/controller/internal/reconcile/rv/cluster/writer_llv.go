package cluster

import (
	"fmt"

	"github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type LLVWriterImpl struct {
	RVNodeAdapter
	actualLVNameOnTheNode string
}

var _ LLVWriter = &LLVWriterImpl{}

func NewLLVBuilder(rvNode RVNodeAdapter) (*LLVWriterImpl, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}
	if rvNode.Diskless() {
		return nil, errArg("expected diskful node, got diskless")
	}

	return &LLVWriterImpl{
		RVNodeAdapter: rvNode,
	}, nil
}

type LLVInitializer func(llv *snc.LVMLogicalVolume) error

func (w *LLVWriterImpl) SetActualLVNameOnTheNode(actualLVNameOnTheNode string) {
	w.actualLVNameOnTheNode = actualLVNameOnTheNode
}

func (w *LLVWriterImpl) WriteToLLV(llv *snc.LVMLogicalVolume) (ChangeSet, error) {

	cs := ChangeSet{}

	cs = Change(cs, "actualLVNameOnTheNode", &llv.Spec.ActualLVNameOnTheNode, w.actualLVNameOnTheNode)
	cs = Change(cs, "size", &llv.Spec.Size, resource.NewQuantity(int64(w.Size()), resource.BinarySI).String())
	cs = Change(cs, "lvmVolumeGroupName", &llv.Spec.LVMVolumeGroupName, w.LVGName())
	cs = Change(cs, "type", &llv.Spec.Type, w.LVMType())

	switch llv.Spec.Type {
	case "Thin":
		cs = ChangeDeepEqual(
			cs,
			"thin",
			&llv.Spec.Thin,
			&snc.LVMLogicalVolumeThinSpec{PoolName: w.LVGThinPoolName()},
		)
		cs = ChangeDeepEqual(cs, "thick", &llv.Spec.Thick, nil)
	case "Thick":
		cs = ChangeDeepEqual(cs, "thin", &llv.Spec.Thin, nil)
		cs = ChangeDeepEqual(
			cs,
			"thick",
			&llv.Spec.Thick,
			&snc.LVMLogicalVolumeThickSpec{
				// TODO: make this configurable
				Contiguous: utils.Ptr(false),
			},
		)
	default:
		return cs, fmt.Errorf("expected either Thin or Thick LVG type, got: %s", llv.Spec.Type)
	}

	// TODO: support VolumeCleanup
	return cs, nil
}
