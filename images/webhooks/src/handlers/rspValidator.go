/*
Copyright 2024 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handlers

import (
	"context"
	"fmt"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"slices"
)

const (
	LVMThinType = "LVMThin"
)

func RSPValidate(ctx context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	rsp, ok := obj.(*srv.ReplicatedStoragePool)
	if !ok {
		// If not a storage class just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{}, nil
	}

	cl, err := NewKubeClient("")
	if err != nil {
		klog.Fatal(err)
	}

	listDevice := &snc.LvmVolumeGroupList{}

	err = cl.List(ctx, listDevice)
	if err != nil {
		klog.Fatal(err)
	}

	errMsg := ""
	var lvmVolumeGroupUnique []string

	for _, rspLVMvg := range rsp.Spec.LvmVolumeGroups {
		lvgExists := false
		thinPoolExists := false

		if slices.Contains(lvmVolumeGroupUnique, rspLVMvg.Name) {
			errMsg = fmt.Sprintf("There must be unique LVMVolumeGroup names (%s duplicates)", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		lvmVolumeGroupUnique = append(lvmVolumeGroupUnique, rspLVMvg.Name)

		if rsp.Spec.Type == LVMThinType && rspLVMvg.ThinPoolName == "" {
			errMsg = fmt.Sprintf("ThinPoolName must be set for LVMThin type in LVMVolumeGroup %s; ", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		for _, lvmVG := range listDevice.Items {
			if lvmVG.Name == rspLVMvg.Name {
				lvgExists = true
				if rsp.Spec.Type == LVMThinType {
					for _, thinPool := range lvmVG.Spec.ThinPools {
						if thinPool.Name == rspLVMvg.ThinPoolName {
							thinPoolExists = true
							break
						}
					}
				}
				break
			}
		}

		if !lvgExists {
			errMsg = fmt.Sprintf("LVMVolumeGroup %s not found; ", rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}

		if rsp.Spec.Type == LVMThinType && !thinPoolExists {
			errMsg = fmt.Sprintf("ThinPool %s not found in LVMVolumeGroup %s; ", rspLVMvg.ThinPoolName, rspLVMvg.Name)
			klog.Info(errMsg)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: errMsg},
				nil
		}
	}

	return &kwhvalidating.ValidatorResult{Valid: true},
		nil
}
