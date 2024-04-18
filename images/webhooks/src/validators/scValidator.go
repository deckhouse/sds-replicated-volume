/*
Copyright 2023 Flant JSC

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
	"encoding/json"
	"net/http"

	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

func SCValidate(w http.ResponseWriter, r *http.Request) {
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

	var jsonData map[string]interface{}
	json.Unmarshal(raw, &jsonData)

	if jsonData["provisioner"] == "linstor.csi.linbit.com" {
		if arReview.Request.UserInfo.Username == "system:serviceaccount:d8-sds-replicated-volume:sds-replicated-volume-controller" {
			arReview.Response.Allowed = true
			klog.Infof("Incoming request approved (%s)", string(raw))
		} else if arReview.Request.Operation == "DELETE" {
			arReview.Response.Allowed = true
			klog.Infof("Incoming request approved (%s)", string(raw))
		} else {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: "Manual operations with this StorageClass is prohibited. Please use ReplicatedStorageClass instead.",
			}
			klog.Infof("Incoming request denied: Manual operations with this StorageClass is prohibited. Please use ReplicatedStorageClass instead (%s)", string(raw))
		}
	} else {
		arReview.Response.Allowed = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&arReview)
}
