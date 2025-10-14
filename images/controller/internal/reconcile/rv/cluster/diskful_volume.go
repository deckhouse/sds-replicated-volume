package cluster

import (
	"context"
	"fmt"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type diskfulVolume struct {
	ctx      context.Context
	llvCl    LLVClient
	rvrCl    RVRClient
	minorMgr MinorManager
	props    diskfulVolumeProps
	dprops   diskfulVolumeDynamicProps
}

var _ volume = &diskfulVolume{}

type diskfulVolumeProps struct {
	rvName                string
	nodeName              string
	id                    int
	vgName                string
	actualVGNameOnTheNode string
	size                  int64
	llvProps              LLVProps
}

type diskfulVolumeDynamicProps struct {
	actualVGNameOnTheNode string
	actualLVNameOnTheNode string
	minor                 uint
	existingLLV           *snc.LVMLogicalVolume
	existingLLVSizeQty    resource.Quantity
}

func (v *diskfulVolume) initialize(existingRVRVolume *v1alpha2.Volume) error {
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

	existingLLV, err := v.llvCl.ByActualLVNameOnTheNode(
		v.ctx,
		v.props.nodeName,
		v.dprops.actualLVNameOnTheNode,
	)
	if err != nil {
		return err
	}

	if existingLLV == nil {
		// support volumes migrated from LINSTOR
		existingLLV, err = v.llvCl.ByActualLVNameOnTheNode(
			v.ctx,
			v.props.nodeName,
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

func (v *diskfulVolume) reconcile() (Action, bool, error) {
	// TODO: do not recreate LLV, recreate replicas
	// TODO: discuss that Failed LLV may lead to banned nodes
	if v.dprops.existingLLV != nil {
		return v.reconcileLLV()
	} else {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: v1.ObjectMeta{
				GenerateName: fmt.Sprintf("%s-", v.props.rvName),
				Finalizers:   []string{ControllerFinalizerName},
			},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: v.dprops.actualLVNameOnTheNode,
				Size:                  resource.NewQuantity(v.props.size, resource.BinarySI).String(),
				LVMVolumeGroupName:    v.props.vgName,
			},
		}

		v.props.llvProps.applyToLLV(&llv.Spec)

		return Actions{
			CreateLVMLogicalVolume{LVMLogicalVolume: llv},
			WaitLVMLogicalVolume{llv},
		}, false, nil
	}
}

func (v *diskfulVolume) rvrVolume() v1alpha2.Volume {
	rvrVolume := v1alpha2.Volume{
		Number: uint(v.props.id),
		Device: v.dprops.minor,
	}

	rvrVolume.SetDisk(v.dprops.actualVGNameOnTheNode, v.dprops.actualLVNameOnTheNode)

	return rvrVolume
}

func (v *diskfulVolume) reconcileLLV() (Action, bool, error) {
	desired := resource.NewQuantity(v.props.size, resource.BinarySI)
	actual, err := resource.ParseQuantity(v.dprops.existingLLV.Spec.Size)

	if err != nil {
		return nil, false, fmt.Errorf(
			"parsing LLV %s spec size '%s': %w",
			v.dprops.existingLLV.Name, v.dprops.existingLLV.Spec.Size, err,
		)
	}

	if actual.Cmp(*desired) >= 0 {
		return nil, false, nil
	}

	return Actions{
		LLVPatch{
			LVMLogicalVolume: v.dprops.existingLLV,
			Apply: func(llv *snc.LVMLogicalVolume) error {
				desired := resource.NewQuantity(v.props.size, resource.BinarySI)
				actual, err := resource.ParseQuantity(llv.Spec.Size)
				if err != nil {
					return err
				}

				if actual.Cmp(*desired) >= 0 {
					return nil
				}
				llv.Spec.Size = desired.String()
				return nil
			},
		},
		WaitLVMLogicalVolume{
			LVMLogicalVolume: v.dprops.existingLLV,
		},
	}, true, nil
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

func (v *diskfulVolume) shouldBeRecreated(rvrVol *v1alpha2.Volume) bool {
	if int(rvrVol.Number) != v.props.id {
		return true
	}
	if v.dprops.actualVGNameOnTheNode != v.props.actualVGNameOnTheNode {
		return true
	}
	return false
}
