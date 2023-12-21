package validators

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"net/http"
)

type dsc struct {
	Spec     dscSpec  `json:"spec"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type dscSpec struct {
	IsDefault       bool   `json:"isDefault"`
	ReclaimPolicy   string `json:"reclaimPolicy"`
	Replication     string `json:"replication"`
	StoragePool     string `json:"storagePool"`
	PreferablyLocal string `json:"PreferablyLocal"`
}

func DSCValidate(w http.ResponseWriter, r *http.Request) {
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

	dscJson := dsc{}
	json.Unmarshal(raw, &dscJson)

	if dscJson.Spec.IsDefault == true {
		config, err := rest.InClusterConfig()
		if err != nil {
			klog.Fatal(err.Error())
		}

		client, err := dynamic.NewForConfig(config)
		if err != nil {
			klog.Fatal(err)
		}

		dscRes := schema.GroupVersionResource{Group: "storage.deckhouse.io", Version: "v1alpha1", Resource: "drbdstorageclasses"}

		ctx := context.Background()

		listedResources, err := client.Resource(dscRes).List(ctx, metav1.ListOptions{})
		if err != nil {
			klog.Fatal(err)
		}

		for _, item := range listedResources.Items {
			listedDscClassName, _, _ := unstructured.NestedString(item.Object, "metadata", "name")
			listedDscDefaultState, _, _ := unstructured.NestedBool(item.Object, "spec", "isDefault")
			if listedDscClassName != dscJson.Metadata.Name && listedDscDefaultState == true {
				arReview.Response.Allowed = false
				arReview.Response.Result = &metav1.Status{
					Message: fmt.Sprintf("Default DRBDStorageClass already set: %s", listedDscClassName),
				}
			}
		}

	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&arReview)
}
