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
	"strings"
	"testing"

	"github.com/slok/kubewebhook/v2/pkg/model"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/deckhouse/sds-common-lib/fsreserve"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func bytePtr(v byte) *byte { return &v }

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
			name: "Consistency replication is rejected",
			spec: srv.ReplicatedStorageClassSpec{
				Replication: "Consistency", //nolint:staticcheck // SA1019: the validator exists to handle the deprecated Replication field
			},
			wantValid:   false,
			wantMessage: "replication Consistency is not supported by legacy control plane",
		},
		{
			name: "ConsistencyAndAvailability replication is valid",
			spec: srv.ReplicatedStorageClassSpec{
				Replication: "ConsistencyAndAvailability", //nolint:staticcheck // SA1019: the validator exists to handle the deprecated Replication field
			},
			wantValid: true,
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
			spec:      srv.ReplicatedStorageClassSpec{StoragePool: "test-pool"}, //nolint:staticcheck // SA1019: the validator exists to handle the deprecated StoragePool field
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
			wantMessage:  "with replication set to Availability or ConsistencyAndAvailability (the default), three zones need to be specified",
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
			wantMessage:  "with replication set to Availability or ConsistencyAndAvailability (the default), three zones need to be specified",
		},
		{
			name:         "TransZonal unset replication 3 zones",
			topology:     "TransZonal",
			zones:        []string{"a", "b", "c"},
			clusterZones: []string{"a", "b", "c"},
			wantValid:    true,
		},
		{
			name:         "TransZonal unset replication 4 zones rejected",
			topology:     "TransZonal",
			zones:        []string{"a", "b", "c", "d"},
			clusterZones: []string{"a", "b", "c", "d"},
			wantValid:    false,
			wantMessage:  "with replication set to Availability or ConsistencyAndAvailability (the default), three zones need to be specified",
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
					Replication: tt.replication, //nolint:staticcheck // SA1019: the validator exists to handle the deprecated Replication field
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

// A class being deleted is let through without any check, so the controller
// can remove its finalizer even when the class no longer passes validation.
// The call must not reach the cluster: there is none in a unit test.
func TestRSCValidate_DeletingClassIsNotValidated(t *testing.T) {
	now := metav1.Now()
	rsc := &srv.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "r2-ignored",
			DeletionTimestamp: &now,
			Finalizers:        []string{"replicatedstorageclass.storage.deckhouse.io"},
			Annotations:       map[string]string{fsreserve.AnnotationKey: "not-a-number"},
		},
		Spec: srv.ReplicatedStorageClassSpec{
			Topology: topologyIgnored,
			Zones:    []string{"zone-a"},
		},
	}

	got, err := RSCValidate(context.Background(), &model.AdmissionReview{Operation: model.OperationUpdate}, rsc)
	if err != nil {
		t.Fatalf("RSCValidate() error = %v", err)
	}
	if !got.Valid {
		t.Fatalf("RSCValidate() = %+v, want a class being deleted to be valid", got)
	}
}

func Test_validateStorageClassName(t *testing.T) {
	scWith := func(name, provisioner string) *storagev1.StorageClass {
		return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Provisioner: provisioner}
	}

	tests := []struct {
		name      string
		objects   []runtime.Object
		getErr    error
		wantValid bool
		wantErr   bool
	}{
		{
			name:      "free name",
			wantValid: true,
		},
		{
			name:      "StorageClass of the replicated provisioner",
			objects:   []runtime.Object{scWith("rsc", replicatedCSIProvisioner)},
			wantValid: true,
		},
		{
			name:      "StorageClass of another provisioner",
			objects:   []runtime.Object{scWith("rsc", "e2e.example.com/foreign")},
			wantValid: false,
		},
		{
			name:    "StorageClass read fails",
			getErr:  apierrors.NewServiceUnavailable("apiserver is down"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := fake.NewClientset(tt.objects...)
			if tt.getErr != nil {
				cl.PrependReactor("get", "storageclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.getErr
				})
			}

			got, err := validateStorageClassName(context.Background(), cl, "rsc")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateStorageClassName() = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateStorageClassName() error = %v", err)
			}
			if got.Valid != tt.wantValid {
				t.Fatalf("Valid = %v, want %v (message %q)", got.Valid, tt.wantValid, got.Message)
			}
			if !tt.wantValid && !strings.Contains(got.Message, "e2e.example.com/foreign") {
				t.Errorf("Message = %q, want it to name the provisioner", got.Message)
			}
		})
	}
}
