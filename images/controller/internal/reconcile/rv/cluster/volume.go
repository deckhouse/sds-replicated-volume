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
	dprops   volumeDynamicProps
}

type volumeProps struct {
	rvName                string
	nodeName              string
	id                    int
	vgName                string
	actualVGNameOnTheNode string
	size                  int64
}

type volumeDynamicProps struct {
	actualVGNameOnTheNode string
	actualLVNameOnTheNode string
	minor                 uint
	existingLLV           *snc.LVMLogicalVolume
	existingLLVSizeQty    resource.Quantity
}

func (v *volume) Initialize(existingRVRVolume *v1alpha2.Volume) error {
	if existingRVRVolume == nil {
		v.dprops.actualVGNameOnTheNode = v.props.actualVGNameOnTheNode
		v.dprops.actualLVNameOnTheNode = v.props.rvName

		// minor
		minor, err := v.minorMgr.ReserveNodeMinor(v.ctx, v.props.nodeName)
		if err != nil {
			return err
		}
		v.dprops.minor = minor
	} else {
		aVGName, aLVName, err := existingRVRVolume.ParseDisk()
		if err != nil {
			return err
		}
		v.dprops.actualVGNameOnTheNode = aVGName
		v.dprops.actualLVNameOnTheNode = aLVName

		// minor
		v.dprops.minor = existingRVRVolume.Device
	}

	existingLLV, err := v.llvCl.ByActualNamesOnTheNode(
		v.ctx,
		v.props.nodeName,
		v.dprops.actualVGNameOnTheNode,
		v.dprops.actualLVNameOnTheNode,
	)
	if err != nil {
		return err
	}

	if existingLLV == nil {
		// support volumes migrated from LINSTOR
		// TODO: check suffix
		existingLLV, err = v.llvCl.ByActualNamesOnTheNode(
			v.ctx,
			v.props.nodeName,
			v.props.actualVGNameOnTheNode,
			v.dprops.actualLVNameOnTheNode+"_00000",
		)
		if err != nil {
			return err
		}
	}

	if existingLLV != nil {
		llvSizeQty, err := resource.ParseQuantity(existingLLV.Spec.Size)
		if err != nil {
			return fmt.Errorf("parsing the size of llv %s: %w", existingLLV.Name, err)
		}
		v.dprops.existingLLVSizeQty = llvSizeQty
	}

	v.dprops.existingLLV = existingLLV

	return nil
}

func (v *volume) Reconcile() Action {
	// TODO: do not recreate LLV, recreate replicas
	// TODO: discuss that Failed LLV may lead to banned nodes
	if v.dprops.existingLLV != nil {
		return v.reconcileLLV()
	} else {
		llv := &snc.LVMLogicalVolume{
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: v.dprops.actualLVNameOnTheNode,
				Size:                  resource.NewQuantity(v.props.size, resource.BinarySI).String(),
				// TODO: check these props and pass them
				Type:               "Thick",
				LVMVolumeGroupName: v.props.vgName,
			},
		}

		return Actions{
			CreateLVMLogicalVolume{LVMLogicalVolume: llv},
			WaitLVMLogicalVolume{llv},
		}
	}
}

func (v *volume) RVRVolume() v1alpha2.Volume {
	rvrVolume := v1alpha2.Volume{
		Number: uint(v.props.id),
		Device: v.dprops.minor,
	}

	rvrVolume.SetDisk(v.dprops.actualVGNameOnTheNode, v.dprops.actualLVNameOnTheNode)

	return rvrVolume
}

func (v *volume) reconcileLLV() Action {
	// Always produce a patch action when LLV exists so higher layers can
	// reconcile desired properties (size and others) deterministically.
	// If no change is needed, the patch becomes a no-op.
	return LLVPatch{LVMLogicalVolume: v.dprops.existingLLV, Apply: func(llv *snc.LVMLogicalVolume) error {
		// Resize only when a positive desired size is specified and differs
		// from the current one. Otherwise, leave as is (no-op patch).
		if v.props.size > 0 {
			desired := resource.NewQuantity(v.props.size, resource.BinarySI).String()
			// TODO only increase
			if llv.Spec.Size != desired {
				llv.Spec.Size = desired
			}
		}
		return nil
	}}

	// TODO
	// type LVMLogicalVolumeSpec struct {
	// 	ActualLVNameOnTheNode string                     `json:"actualLVNameOnTheNode"`   // -
	// 	Type                  string                     `json:"type"`                    // -
	// 	Size                  string                     `json:"size"`                    // +
	// 	LVMVolumeGroupName    string                     `json:"lvmVolumeGroupName"`      // recreate
	// 	Source                *LVMLogicalVolumeSource    `json:"source"`                  // -
	// 	Thin                  *LVMLogicalVolumeThinSpec  `json:"thin"`                    // +TODO: добавляем в RV lvmVolumeGroups
	// 	Thick                 *LVMLogicalVolumeThickSpec `json:"thick"`                   // +
	// 	VolumeCleanup         *string                    `json:"volumeCleanup,omitempty"` // + (fix maybe?)
	// }

}

func (v *volume) ShouldBeRecreated(rvrVol *v1alpha2.Volume) bool {
	if int(rvrVol.Number) != v.props.id {
		return true
	}
	if v.dprops.actualVGNameOnTheNode != v.props.actualVGNameOnTheNode {
		return true
	}
	return false
}
