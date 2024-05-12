package validators

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type rsc struct {
	Spec     rscSpec  `json:"spec"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type rscSpec struct {
	IsDefault       bool     `json:"isDefault"`
	ReclaimPolicy   string   `json:"reclaimPolicy"`
	Replication     string   `json:"replication"`
	StoragePool     string   `json:"storagePool"`
	PreferablyLocal string   `json:"PreferablyLocal"`
	Topology        string   `json:"topology"`
	Zones           []string `json:"zones"`
}

func RSCValidate(w http.ResponseWriter, r *http.Request) {
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

	rscJson := rsc{}
	json.Unmarshal(raw, &rscJson)

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal(err.Error())
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

	if rscJson.Spec.Topology == "TransZonal" {
		if len(rscJson.Spec.Zones) == 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("You must set at least one zone."),
			}
			klog.Infof("No zones in ReplicatedStorageClass (%s)", string(raw))
		}
		if (rscJson.Spec.Replication == "Availability" || rscJson.Spec.Replication == "ConsistencyAndAvailability") && len(rscJson.Spec.Zones) != 3 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("With replication set to Availability or ConsistencyAndAvailability, three zones need to be specified."),
			}
			klog.Infof("Incorrect combination of replication and zones (%s)", string(raw))
		}

		if len(clusterZoneList) == 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("TransZonal topology denied in cluster without zones. Use Ignored instead."),
			}
			klog.Infof("TransZonal topology denied in cluster without zones. Use Ignored instead (%s)", string(raw))
		}
	} else if rscJson.Spec.Topology == "Zonal" {
		if len(rscJson.Spec.Zones) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("No zones must be set with Zonal topology."),
			}
			klog.Infof("No zones must be set with Zonal topology (%s)", string(raw))
		}

		if len(clusterZoneList) == 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("Zonal topology denied in cluster without zones. Use Ignored instead."),
			}
			klog.Infof("Zonal topology denied in cluster without zones. Use Ignored instead (%s)", string(raw))
		}
	} else if rscJson.Spec.Topology == "Ignored" {
		if len(clusterZoneList) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("In a cluster with existing zones, the Ignored topology should not be used."),
			}
			klog.Infof("In a cluster with existing zones, the Ignored topology should not be used (%s)", string(raw))
		}
		if len(rscJson.Spec.Zones) != 0 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("No zones must be set with Ignored topology."),
			}
			klog.Infof("No zones must be set with Ignored topology (%s)", string(raw))
		}
	}

	if rscJson.Spec.IsDefault == true {
		storageClasses, _ := staticClient.StorageV1().StorageClasses().List(context.Background(), metav1.ListOptions{})
		for _, storageClass := range storageClasses.Items {
			for label, value := range storageClass.GetObjectMeta().GetAnnotations() {
				if label == "storageclass.kubernetes.io/is-default-class" && value == "true" && storageClass.Name != rscJson.Metadata.Name {
					arReview.Response.Allowed = false
					arReview.Response.Result = &metav1.Status{
						Message: fmt.Sprintf("Default ReplicatedStorageClass already set: %s", storageClass.Name),
					}
					klog.Infof("Default StorageClass already set: %s (%s)", storageClass.Name, string(raw))
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&arReview)
}
