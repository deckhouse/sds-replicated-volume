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

package validators

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	sdsnc "webhooks/api/sds-node-configurator"
	"webhooks/funcs"

	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	LVMThinType = "LVMThin"
)

type ReplicatedStoragePool struct {
	Spec     ReplicatedStoragePoolSpec `json:"spec"`
	Metadata Metadata                  `json:"metadata"`
}

type ReplicatedStoragePoolSpec struct {
	Type            string                                 `json:"type"`
	LvmVolumeGroups []ReplicatedStoragePoolLVMVolumeGroups `json:"lvmVolumeGroups"`
}

type ReplicatedStoragePoolLVMVolumeGroups struct {
	Name         string `json:"name"`
	ThinPoolName string `json:"thinPoolName"`
}

func RSPValidate(w http.ResponseWriter, r *http.Request) {

	arReview := v1beta1.AdmissionReview{}
	if err := json.NewDecoder(r.Body).Decode(&arReview); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if arReview.Request == nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	raw := arReview.Request.Object.Raw

	arReview.Response = &v1beta1.AdmissionResponse{
		UID:     arReview.Request.UID,
		Allowed: true,
	}

	rspJson := ReplicatedStoragePool{}
	err := json.Unmarshal(raw, &rspJson)
	if err != nil {
		klog.Errorf("Error unmarshalling RSP: %s", err)
		arReview.Response.Allowed = false
		arReview.Response.Result = &metav1.Status{
			Message: fmt.Sprintf("Error unmarshalling RSP: %s", err),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&arReview)
		return
	}

	cl, _ := funcs.NewKubeClient()
	ctx := context.Background()

	lvmVGs := &sdsnc.LvmVolumeGroupList{}

	err = cl.List(ctx, lvmVGs, &client.ListOptions{})
	if err != nil {
		klog.Errorf("Error getting LVMVolumeGroups: %s", err)
		arReview.Response.Allowed = false
		arReview.Response.Result = &metav1.Status{
			Message: fmt.Sprintf("Error getting LVMVolumeGroups: %s", err),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(&arReview)
		return
	}

	rspType := rspJson.Spec.Type
	resultMsg := ""

	for _, rspLVMvg := range rspJson.Spec.LvmVolumeGroups {
		lvgExists := false
		thinPoolExists := false

		if rspLVMvg.Name == "" {
			arReview.Response.Allowed = false
			resultMsg += fmt.Sprintf("Name must be set for LVMVolumeGroup; ")
			klog.Infof("Name must be set for LVMVolumeGroup (%s)", string(raw))
		}

		if rspType == LVMThinType {
			if rspLVMvg.ThinPoolName == "" {
				arReview.Response.Allowed = false
				resultMsg += fmt.Sprintf("ThinPoolName must be set for LVMThin type in LVMVolumeGroup %s; ", rspLVMvg.Name)
				klog.Infof("ThinPoolName must be set for LVMThin type (%s)", string(raw))
			}
		}

		for _, lvmVG := range lvmVGs.Items {
			if lvmVG.Name == rspLVMvg.Name {
				lvgExists = true
				if rspType == LVMThinType {
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
			arReview.Response.Allowed = false
			resultMsg += fmt.Sprintf("LVMVolumeGroup %s not found; ", rspLVMvg.Name)
			klog.Infof("LVMVolumeGroup %s not found (%s)", rspLVMvg.Name, string(raw))
		}

		if rspType == LVMThinType {
			if !thinPoolExists {
				arReview.Response.Allowed = false
				resultMsg += fmt.Sprintf("ThinPool %s not found in LVMVolumeGroup %s; ", rspLVMvg.ThinPoolName, rspLVMvg.Name)
				klog.Infof("ThinPool %s not found in LVMVolumeGroup %s (%s)", rspLVMvg.ThinPoolName, rspLVMvg.Name, string(raw))
			}
		}
	}

	if arReview.Response.Allowed == false {
		arReview.Response.Result = &metav1.Status{
			Message: resultMsg,
		}
		klog.Infof("Incoming request denied: %s", resultMsg)
	} else {
		klog.Infof("Incoming request approved (%s)", string(raw))
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(&arReview)
	if err != nil {
		klog.Errorf("Error encoding response: %s", err)
		http.Error(w, fmt.Sprintf("Could not encode response: %v", err), http.StatusInternalServerError)
	}
}
