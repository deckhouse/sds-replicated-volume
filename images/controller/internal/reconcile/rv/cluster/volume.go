package cluster

import (
	"context"
	"fmt"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"k8s.io/apimachinery/pkg/api/resource"
)

type volume struct {
	ctx      context.Context
	llvCl    LLVClient
	rvrCl    RVRClient
	minorMgr MinorManager
	props    volumeProps
}

type volumeProps struct {
	rvName                string
	nodeName              string
	id                    int
	vgName                string
	actualVGNameOnTheNode string
	size                  int64
}

func (v *volume) Initialize(rvrVolume *v1alpha2.Volume) (Action, error) {
	minor, err := v.minorMgr.ReserveNodeMinor(v.ctx, v.props.nodeName)
	if err != nil {
		return nil, err
	}

	existingLLV, err := v.llvCl.ByActualNamesOnTheNode(v.props.nodeName, v.props.actualVGNameOnTheNode, v.props.rvName)
	if err != nil {
		return nil, err
	}

	if existingLLV == nil {
		// support volumes migrated from LINSTOR
		// TODO: check suffix
		existingLLV, err = v.llvCl.ByActualNamesOnTheNode(v.props.nodeName, v.props.actualVGNameOnTheNode, v.props.rvName+"_000000")
		if err != nil {
			return nil, err
		}
	}

	var action Action
	actualLVNameOnTheNode := v.props.rvName
	if existingLLV != nil {
		action, err = v.reconcileLLV(existingLLV)
		actualLVNameOnTheNode = existingLLV.Spec.ActualLVNameOnTheNode
	} else {
		llv := &snc.LVMLogicalVolume{
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: actualLVNameOnTheNode,
				Size:                  resource.NewQuantity(v.props.size, resource.BinarySI).String(),
				// TODO: check these props and pass them
				Type:               "Thick",
				LVMVolumeGroupName: v.props.vgName,
			},
		}

		action = Actions{
			CreateLVMLogicalVolume{LVMLogicalVolume: llv},
			WaitLVMLogicalVolume{llv},
		}
	}

	*rvrVolume = v1alpha2.Volume{
		Number: uint(v.props.id),
		Disk: fmt.Sprintf(
			"/dev/%s/%s",
			v.props.actualVGNameOnTheNode, actualLVNameOnTheNode,
		),
		Device: minor,
	}

	return action, nil
}

func (v *volume) reconcileLLV(llv *snc.LVMLogicalVolume) (Action, error) {
	llvSizeQty, err := resource.ParseQuantity(llv.Spec.Size)
	if err != nil {
		return nil, fmt.Errorf("parsing the size of llv %s: %w", llv.Name, err)
	}

	cmp := llvSizeQty.CmpInt64(v.props.size)
	if cmp < 0 {
		return Patch[*snc.LVMLogicalVolume](func(llv *snc.LVMLogicalVolume) error {
			llv.Spec.Size = resource.NewQuantity(v.props.size, resource.BinarySI).String()
			return nil
		}), nil
	}

	// TODO reconcile other props

	return nil, nil
}

// func (v *volume) IsValid(rvrVol *v1alpha2.Volume) (bool, string) {
// 	if int(rvrVol.Number) != v.props.id {
// 		return false,
// 			fmt.Sprintf(
// 				"expected volume number %d, go %d",
// 				v.props.id, rvrVol.Number,
// 			)
// 	}

// 	// rvrVol.Device
// }
