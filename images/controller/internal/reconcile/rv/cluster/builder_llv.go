package cluster

import (
	"fmt"

	"github.com/deckhouse/sds-common-lib/utils"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type LLVBuilder struct {
	RVNodeAdapter
	actualLVNameOnTheNode string
}

func NewLLVBuilder(rvNode RVNodeAdapter) (*LLVBuilder, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}
	if rvNode.Diskless() {
		return nil, errArg("expected diskful node, got diskless")
	}

	return &LLVBuilder{
		RVNodeAdapter: rvNode,
	}, nil
}

type LLVInitializer func(llv *snc.LVMLogicalVolume) error

func (b *LLVBuilder) SetActualLVNameOnTheNode(actualLVNameOnTheNode string) {
	b.actualLVNameOnTheNode = actualLVNameOnTheNode
}

func (b *LLVBuilder) BuildInitializer() LLVInitializer {
	return func(llv *snc.LVMLogicalVolume) error {
		llv.Spec.ActualLVNameOnTheNode = b.actualLVNameOnTheNode
		llv.Spec.Size = resource.NewQuantity(int64(b.Size()), resource.BinarySI).String()
		llv.Spec.LVMVolumeGroupName = b.LVGName()

		llv.Spec.Type = b.LVMType()

		switch llv.Spec.Type {
		case "Thin":
			llv.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
				PoolName: b.LVGThinPoolName(),
			}
		case "Thick":
			llv.Spec.Thick = &snc.LVMLogicalVolumeThickSpec{
				// TODO: make this configurable
				Contiguous: utils.Ptr(true),
			}
		default:
			return fmt.Errorf("expected either Thin or Thick LVG type, got: %s", llv.Spec.Type)
		}

		// TODO: support VolumeCleanup
		return nil
	}
}
