package cluster

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type LLVProps interface {
	applyToLLV(spec *snc.LVMLogicalVolumeSpec)
}

type ThinVolumeProps struct {
	PoolName string
}

type ThickVolumeProps struct {
	Contigous *bool
}

var _ LLVProps = ThinVolumeProps{}
var _ LLVProps = ThickVolumeProps{}

func (p ThinVolumeProps) applyToLLV(spec *snc.LVMLogicalVolumeSpec) {
	spec.Type = "Thin"
	spec.Thin = &snc.LVMLogicalVolumeThinSpec{
		PoolName: p.PoolName,
	}
}

func (p ThickVolumeProps) applyToLLV(spec *snc.LVMLogicalVolumeSpec) {
	spec.Type = "Thick"
	spec.Thick = &snc.LVMLogicalVolumeThickSpec{
		Contiguous: p.Contigous,
	}
}
