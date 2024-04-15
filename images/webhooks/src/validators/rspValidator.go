package validators

import (
	"context"
	"encoding/json"
	"fmt"
	dhctl "github.com/deckhouse/deckhouse/deckhouse-controller/pkg/apis/deckhouse.io/v1alpha1"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"net/http"
	rsva1alpha1 "webhooks/api/v1alpha1"
	"webhooks/funcs"
)

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

	rspJson := rsva1alpha1.ReplicatedStoragePool{}
	json.Unmarshal(raw, &rspJson)

	if rspJson.Spec.Type == "LVMThin" {
		ctx := context.Background()
		cl, err := funcs.NewKubeClient()

		if err != nil {
			klog.Fatal(err.Error())
		}

		objs := dhctl.ModuleConfigList{}

		err = cl.List(ctx, &objs)
		if err != nil {
			klog.Fatal(err.Error())
		}

		for _, obj := range objs.Items {
			if obj.Name == "sds-replicated-volume" {
				if obj.Spec.Settings == nil {
					arReview.Response.Allowed = false
					arReview.Response.Result = &metav1.Status{
						Message: fmt.Sprintf("Thin pools must be enabled in sds-replicated-volume."),
					}
					klog.Infof("thin pools must be enabled in sds-replicated-volume (%s)", string(raw))
				} else {
					if obj.Spec.Settings["enableThinProvisioning"] == false {
						arReview.Response.Allowed = false
						arReview.Response.Result = &metav1.Status{
							Message: fmt.Sprintf("Thin pools must be enabled in sds-replicated-volume."),
						}
						klog.Infof("thin pools must be enabled in sds-replicated-volume (%s)", string(raw))
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&arReview)
}
