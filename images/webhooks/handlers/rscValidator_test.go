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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func Test_validateLegacySpecFields(t *testing.T) {
	tests := []struct {
		name        string
		spec        srv.ReplicatedStorageClassSpec
		wantValid   bool
		wantMessage string
	}{
		{
			name:      "empty spec is valid",
			spec:      srv.ReplicatedStorageClassSpec{},
			wantValid: true,
		},
		{
			name: "FailuresToTolerate set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				FailuresToTolerate: func() *byte { v := byte(1); return &v }(),
			},
			wantValid:   false,
			wantMessage: "failuresToTolerate/guaranteedMinimumDataRedundancy cannot be set for legacy control plane; use replication field instead",
		},
		{
			name: "GuaranteedMinimumDataRedundancy set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				GuaranteedMinimumDataRedundancy: func() *byte { v := byte(1); return &v }(),
			},
			wantValid:   false,
			wantMessage: "failuresToTolerate/guaranteedMinimumDataRedundancy cannot be set for legacy control plane; use replication field instead",
		},
		{
			name: "Storage set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				Storage: &srv.ReplicatedStorageClassStorage{
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "test"}},
				},
			},
			wantValid:   false,
			wantMessage: "spec.storage cannot be set for legacy control plane; use storagePool field instead",
		},
		{
			name: "NodeLabelSelector set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				NodeLabelSelector: &metav1.LabelSelector{},
			},
			wantValid:   false,
			wantMessage: "spec.nodeLabelSelector cannot be set for legacy control plane",
		},
		{
			name: "SystemNetworkNames set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				SystemNetworkNames: []string{"test-net"},
			},
			wantValid:   false,
			wantMessage: "spec.systemNetworkNames cannot be set for legacy control plane",
		},
		{
			name: "ConfigurationRolloutStrategy set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: &srv.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: srv.ConfigurationRolloutRollingUpdate,
				},
			},
			wantValid:   false,
			wantMessage: "spec.configurationRolloutStrategy cannot be set for legacy control plane",
		},
		{
			name: "EligibleNodesConflictResolutionStrategy set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				EligibleNodesConflictResolutionStrategy: &srv.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: srv.EligibleNodesConflictResolutionRollingRepair,
				},
			},
			wantValid:   false,
			wantMessage: "spec.eligibleNodesConflictResolutionStrategy cannot be set for legacy control plane",
		},
		{
			name: "EligibleNodesPolicy set is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				EligibleNodesPolicy: &srv.ReplicatedStoragePoolEligibleNodesPolicy{},
			},
			wantValid:   false,
			wantMessage: "spec.eligibleNodesPolicy cannot be set for legacy control plane",
		},
		{
			name:      "minimal valid legacy spec",
			spec:      srv.ReplicatedStorageClassSpec{StoragePool: "test-pool"},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rsc := &srv.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rsc"},
				Spec:       tt.spec,
			}
			result := validateLegacySpecFields(rsc)
			if result.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v", result.Valid, tt.wantValid)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.wantMessage)
			}
		})
	}
}

func Test_validateLegacyControlPlaneTopology(t *testing.T) {
	tests := []struct {
		name         string
		topology     srv.ReplicatedStorageClassTopology
		replication  srv.ReplicatedStorageClassReplication
		zones        []string
		clusterZones []string
		wantValid    bool
		wantMessage  string
	}{
		// TransZonal
		{
			name:         "TransZonal Availability 3 zones",
			topology:     "TransZonal",
			replication:  "Availability",
			zones:        []string{"a", "b", "c"},
			clusterZones: []string{"a", "b", "c"},
			wantValid:    true,
		},
		{
			name:         "TransZonal Availability 2 zones rejected",
			topology:     "TransZonal",
			replication:  "Availability",
			zones:        []string{"a", "b"},
			clusterZones: []string{"a", "b"},
			wantValid:    false,
			wantMessage:  "with replication set to Availability or ConsistencyAndAvailability, three zones need to be specified",
		},
		{
			name:         "TransZonal ConsistencyAndAvailability 3 zones",
			topology:     "TransZonal",
			replication:  "ConsistencyAndAvailability",
			zones:        []string{"a", "b", "c"},
			clusterZones: []string{"a", "b", "c"},
			wantValid:    true,
		},
		{
			name:         "TransZonal ConsistencyAndAvailability 1 zone rejected",
			topology:     "TransZonal",
			replication:  "ConsistencyAndAvailability",
			zones:        []string{"a"},
			clusterZones: []string{"a"},
			wantValid:    false,
			wantMessage:  "with replication set to Availability or ConsistencyAndAvailability, three zones need to be specified",
		},
		{
			name:         "TransZonal Consistency 2 zones allowed",
			topology:     "TransZonal",
			replication:  "Consistency",
			zones:        []string{"a", "b"},
			clusterZones: []string{"a", "b"},
			wantValid:    true,
		},
		{
			name:         "TransZonal None 1 zone allowed",
			topology:     "TransZonal",
			replication:  "None",
			zones:        []string{"a"},
			clusterZones: []string{"a"},
			wantValid:    true,
		},
		{
			name:         "TransZonal no zones in spec",
			topology:     "TransZonal",
			replication:  "Availability",
			clusterZones: []string{"a"},
			wantValid:    false,
			wantMessage:  "you must set at least one zone",
		},
		{
			name:        "TransZonal no zones in cluster",
			topology:    "TransZonal",
			replication: "None",
			zones:       []string{"a"},
			wantValid:   false,
			wantMessage: "transZonal topology denied in cluster without zones; use Ignored instead",
		},
		// Zonal
		{
			name:         "Zonal valid",
			topology:     "Zonal",
			clusterZones: []string{"a"},
			wantValid:    true,
		},
		{
			name:         "Zonal zones set",
			topology:     "Zonal",
			zones:        []string{"a"},
			clusterZones: []string{"a"},
			wantValid:    false,
			wantMessage:  "no zones must be set with Zonal topology",
		},
		{
			name:        "Zonal no cluster zones",
			topology:    "Zonal",
			wantValid:   false,
			wantMessage: "zonal topology denied in cluster without zones; use Ignored instead",
		},
		// Ignored
		{
			name:      "Ignored valid",
			topology:  "Ignored",
			wantValid: true,
		},
		{
			name:         "Ignored cluster has zones",
			topology:     "Ignored",
			clusterZones: []string{"a"},
			wantValid:    false,
			wantMessage:  "in a cluster with existing zones, the Ignored topology should not be used",
		},
		{
			name:        "Ignored zones set in spec",
			topology:    "Ignored",
			zones:       []string{"a"},
			wantValid:   false,
			wantMessage: "no zones must be set with Ignored topology",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rsc := &srv.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rsc"},
				Spec: srv.ReplicatedStorageClassSpec{
					Topology:    tt.topology,
					Replication: tt.replication,
					Zones:       tt.zones,
				},
			}
			result := validateLegacyControlPlaneTopology(rsc, tt.clusterZones)
			if result.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v", result.Valid, tt.wantValid)
			}
			if tt.wantMessage != "" && result.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.wantMessage)
			}
		})
	}
}
