/*
Copyright 2025 Flant JSC

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

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func RSCValidate(_ context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	rsc, ok := obj.(*srv.ReplicatedStorageClass)
	if !ok {
		// If not a storage class just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{}, nil
	}

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

	switch rsc.Spec.Topology {
	case "TransZonal":
		if len(rsc.Spec.Zones) == 0 {
			klog.Infof("No zones in ReplicatedStorageClass (%s)", rsc.Name)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "You must set at least one zone."},
				nil
		}
		if (rsc.Spec.Replication == "Availability" || rsc.Spec.Replication == "ConsistencyAndAvailability") && len(rsc.Spec.Zones) != 3 {
			klog.Infof("Incorrect combination of replication and zones (%s) (%s)", rsc.Spec.Replication, rsc.Spec.Zones)
			return &kwhvalidating.ValidatorResult{Valid: false,
					Message: "With replication set to Availability or ConsistencyAndAvailability, three zones need to be specified."},
				nil
		}

		if len(clusterZoneList) == 0 {
			klog.Infof("TransZonal topology denied in cluster without zones. Use Ignored instead (%s)", rsc.Spec.Topology)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "TransZonal topology denied in cluster without zones. Use Ignored instead."},
				nil
		}
	case "Zonal":
		if len(rsc.Spec.Zones) != 0 {
			klog.Infof("No zones must be set with Zonal topology (%s) (%s)", rsc.Spec.Topology, rsc.Spec.Zones)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "No zones must be set with Zonal topology."},
				nil
		}

		if len(clusterZoneList) == 0 {
			klog.Infof("Zonal topology denied in cluster without zones. Use Ignored instead  (%s) (%s)", rsc.Spec.Topology, rsc.Spec.Zones)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "Zonal topology denied in cluster without zones. Use Ignored instead."},
				nil
		}
	case "Ignored":
		if len(clusterZoneList) != 0 {
			klog.Infof("In a cluster with existing zones, the Ignored topology should not be used  (%s) (%s)", rsc.Spec.Topology, rsc.Spec.Zones)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "In a cluster with existing zones, the Ignored topology should not be used."},
				nil
		}
		if len(rsc.Spec.Zones) != 0 {
			klog.Infof("No zones must be set with Ignored topology  (%s) (%s)", rsc.Spec.Topology, rsc.Spec.Zones)
			return &kwhvalidating.ValidatorResult{Valid: false, Message: "No zones must be set with Ignored topology."},
				nil
		}
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}
