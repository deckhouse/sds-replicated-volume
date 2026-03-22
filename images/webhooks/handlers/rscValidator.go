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
	"fmt"
	"os"

	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	webhookNamespace = "d8-sds-replicated-volume"

	// Topology types for ReplicatedStorageClass.
	topologyTransZonal = "TransZonal"
	topologyZonal      = "Zonal"
	topologyIgnored    = "Ignored"

	// Replication types for ReplicatedStorageClass.
	replicationAvailability               = "Availability"
	replicationConsistencyAndAvailability = "ConsistencyAndAvailability"
)

func RSCValidate(ctx context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error) {
	rsc, ok := obj.(*srv.ReplicatedStorageClass)
	if !ok {
		// If not a storage class just continue the validation chain(if there is one) and do nothing.
		return &kwhvalidating.ValidatorResult{}, nil
	}

	// Determine control plane type based on environment variable.
	isNewControlPlane := os.Getenv("NEW_CONTROL_PLANE") != ""

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	staticClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	if isNewControlPlane {
		// New control plane validations.
		if rsc.Spec.StoragePool != "" { //nolint:staticcheck // legacy StoragePool check for new control plane
			return &kwhvalidating.ValidatorResult{Valid: false,
				Message: "spec.storagePool cannot be set for new control plane; use spec.storage instead"}, nil
		}
		if rsc.Spec.Storage == nil || len(rsc.Spec.Storage.LVMVolumeGroups) == 0 {
			return &kwhvalidating.ValidatorResult{Valid: false,
				Message: "spec.storage is required for new control plane"}, nil
		}
	} else {
		// Legacy control plane validations.
		if r := validateLegacySpecFields(rsc); !r.Valid {
			return r, nil
		}
		if rsc.Spec.StoragePool == "" { //nolint:staticcheck // legacy StoragePool field required by old controller
			return &kwhvalidating.ValidatorResult{Valid: false,
				Message: "spec.storagePool is required for legacy control plane"}, nil
		}

		var clusterZoneList []string
		nodes, err := staticClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, node := range nodes.Items {
			for label, value := range node.GetObjectMeta().GetLabels() {
				if label == "topology.kubernetes.io/zone" {
					clusterZoneList = append(clusterZoneList, value)
				}
			}
		}
		if r := validateLegacyControlPlaneTopology(rsc, clusterZoneList); !r.Valid {
			return r, nil
		}
	}

	return &kwhvalidating.ValidatorResult{Valid: true}, nil
}

// validateLegacySpecFields rejects spec fields that are only for new control plane.
func validateLegacySpecFields(rsc *srv.ReplicatedStorageClass) *kwhvalidating.ValidatorResult {
	spec := &rsc.Spec

	if spec.FailuresToTolerate != nil || spec.GuaranteedMinimumDataRedundancy != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "failuresToTolerate/guaranteedMinimumDataRedundancy cannot be set for legacy control plane; use replication field instead"}
	}
	if spec.Storage != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.storage cannot be set for legacy control plane; use storagePool field instead"}
	}
	if spec.NodeLabelSelector != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.nodeLabelSelector cannot be set for legacy control plane"}
	}
	if len(spec.SystemNetworkNames) > 0 {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.systemNetworkNames cannot be set for legacy control plane"}
	}
	if spec.ConfigurationRolloutStrategy != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.configurationRolloutStrategy cannot be set for legacy control plane"}
	}
	if spec.EligibleNodesConflictResolutionStrategy != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.eligibleNodesConflictResolutionStrategy cannot be set for legacy control plane"}
	}
	if spec.EligibleNodesPolicy != nil {
		return &kwhvalidating.ValidatorResult{Valid: false,
			Message: "spec.eligibleNodesPolicy cannot be set for legacy control plane"}
	}

	return &kwhvalidating.ValidatorResult{Valid: true}
}

// validateLegacyControlPlaneTopology performs full topology validation for legacy control plane.
func validateLegacyControlPlaneTopology(rsc *srv.ReplicatedStorageClass, clusterZoneList []string) *kwhvalidating.ValidatorResult {
	switch rsc.Spec.Topology {
	case topologyTransZonal:
		if len(rsc.Spec.Zones) == 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "you must set at least one zone",
			}
		}
		//nolint:staticcheck // legacy Replication field check for legacy control plane topology validation
		if (rsc.Spec.Replication == replicationAvailability || rsc.Spec.Replication == replicationConsistencyAndAvailability) && len(rsc.Spec.Zones) != 3 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "with replication set to Availability or ConsistencyAndAvailability, three zones need to be specified",
			}
		}
		if len(clusterZoneList) == 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "transZonal topology denied in cluster without zones; use Ignored instead",
			}
		}
	case topologyZonal:
		if len(rsc.Spec.Zones) != 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "no zones must be set with Zonal topology",
			}
		}
		if len(clusterZoneList) == 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "zonal topology denied in cluster without zones; use Ignored instead",
			}
		}
	case topologyIgnored:
		if len(clusterZoneList) != 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "in a cluster with existing zones, the Ignored topology should not be used",
			}
		}
		if len(rsc.Spec.Zones) != 0 {
			return &kwhvalidating.ValidatorResult{
				Valid:   false,
				Message: "no zones must be set with Ignored topology",
			}
		}
	}
	return &kwhvalidating.ValidatorResult{Valid: true}
}
