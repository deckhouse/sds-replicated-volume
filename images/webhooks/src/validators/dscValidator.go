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
	"k8s.io/client-go/kubernetes"
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
	IsDefault       bool     `json:"isDefault"`
	ReclaimPolicy   string   `json:"reclaimPolicy"`
	Replication     string   `json:"replication"`
	StoragePool     string   `json:"storagePool"`
	PreferablyLocal string   `json:"PreferablyLocal"`
	Topology        string   `json:"topology"`
	Zones           []string `json:"zones"`
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

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err.Error())
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	staticClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	var clusterZoneList []string

	nodes, _ := staticClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	for _, node := range nodes.Items {
		for label, value := range node.GetObjectMeta().GetLabels() {
			if label == "topology.kubernetes.io/zone" {
				clusterZoneList = append(clusterZoneList, value)
			}
		}
	}

	if dscJson.Spec.Topology == "TransZonal" {
		if len(dscJson.Spec.Zones) == 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("You must set at least one zone."),
			}
			klog.Infof("No zones in ReplicatedStorageClass (%s)", string(raw))
		}
		if (dscJson.Spec.Replication == "Availability" || dscJson.Spec.Replication == "ConsistencyAndAvailability") && len(dscJson.Spec.Zones) != 3 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("With replication set to Availability or ConsistencyAndAvailability, three zones need to be specified."),
			}
			klog.Infof("Incorrect combination of replication and zones (%s)", string(raw))
		}
	}

	if dscJson.Spec.Topology == "Zonal" {
		if len(dscJson.Spec.Zones) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("No zones must be set with Zonal topology."),
			}
			klog.Infof("No zones must be set with Zonal topology (%s)", string(raw))
		}
	}

	if dscJson.Spec.Topology == "Ignored" {
		if len(clusterZoneList) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("In a cluster with existing zones, the Ignored topology should not be used."),
			}
			klog.Infof("In a cluster with existing zones, the Ignored topology should not be used (%s)", string(raw))
		}
		if len(dscJson.Spec.Zones) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("No zones must be set with Ignored topology."),
			}
			klog.Infof("No zones must be set with Ignored topology (%s)", string(raw))
		}
	}

	if dscJson.Spec.IsDefault == true {
		dscRes := schema.GroupVersionResource{Group: "storage.deckhouse.io", Version: "v1alpha1", Resource: "replicatedstorageclasses"}

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
					Message: fmt.Sprintf("Default ReplicatedStorageClass already set: %s", listedDscClassName),
				}
				klog.Infof("Default ReplicatedStorageClass already set: %s (%s)", listedDscClassName, string(raw))
			} else {
				klog.Infof("Incoming request approved (%s)", string(raw))
			}
		}

	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&arReview)
}
