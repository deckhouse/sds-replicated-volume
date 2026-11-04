/*
Copyright 2026 Flant JSC

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

// This file tests the CEL rules (x-kubernetes-validations) of the generated
// ReplicatedStorageClass CRD for spec.volumeCleanup; see rsc_cel_topology_test.go for why
// they live in this package.

package handlers

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	discardThickOnlyMessage    = "volumeCleanup Discard is supported only for thick volumes"
	volumeCleanupLegacyMessage = "spec.volumeCleanup is not supported by the legacy control plane"
)

// volumeCleanupSpec builds an Ignored-topology spec with the given cleanup method. A non-empty
// storageType makes it a new control plane class over that type, an empty one a legacy class.
func volumeCleanupSpec(storageType srv.ReplicatedStoragePoolType, method srv.VolumeCleanupMethod) srv.ReplicatedStorageClassSpec {
	s := srv.ReplicatedStorageClassSpec{
		ReclaimPolicy: srv.RSCReclaimPolicyDelete,
		Topology:      srv.TopologyIgnored,
		VolumeCleanup: method,
	}
	switch storageType {
	case "":
		s.StoragePool = "legacy-pool" //nolint:staticcheck // legacy control plane RSCs use the deprecated storagePool
	case srv.ReplicatedStoragePoolTypeLVMThin:
		s.Storage = &srv.ReplicatedStorageClassStorage{
			Type:            storageType,
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a", ThinPoolName: "thin-a"}},
		}
	default:
		s.Storage = &srv.ReplicatedStorageClassStorage{
			Type:            storageType,
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a"}},
		}
	}
	return s
}

func Test_RSCVolumeCleanupCEL_Create(t *testing.T) {
	validate := createValidator(t)

	tests := []struct {
		name        string
		storageType srv.ReplicatedStoragePoolType
		method      srv.VolumeCleanupMethod
		wantErr     string
	}{
		{name: "thick without a method", storageType: srv.ReplicatedStoragePoolTypeLVM},
		{name: "thick RandomFillSinglePass", storageType: srv.ReplicatedStoragePoolTypeLVM, method: srv.VolumeCleanupRandomFillSinglePass},
		{name: "thick RandomFillThreePass", storageType: srv.ReplicatedStoragePoolTypeLVM, method: srv.VolumeCleanupRandomFillThreePass},
		{name: "thick Discard", storageType: srv.ReplicatedStoragePoolTypeLVM, method: srv.VolumeCleanupDiscard},
		{name: "thin without a method", storageType: srv.ReplicatedStoragePoolTypeLVMThin},
		{name: "thin RandomFillSinglePass", storageType: srv.ReplicatedStoragePoolTypeLVMThin, method: srv.VolumeCleanupRandomFillSinglePass},
		{name: "thin RandomFillThreePass", storageType: srv.ReplicatedStoragePoolTypeLVMThin, method: srv.VolumeCleanupRandomFillThreePass},
		{name: "thin Discard", storageType: srv.ReplicatedStoragePoolTypeLVMThin, method: srv.VolumeCleanupDiscard, wantErr: discardThickOnlyMessage},
		{name: "legacy without a method"},
		{name: "legacy RandomFillSinglePass", method: srv.VolumeCleanupRandomFillSinglePass, wantErr: volumeCleanupLegacyMessage},
		{name: "legacy Discard", method: srv.VolumeCleanupDiscard, wantErr: volumeCleanupLegacyMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validate(t, volumeCleanupSpec(tt.storageType, tt.method))
			if tt.wantErr == "" {
				if got != "" {
					t.Fatalf("expected the spec to be accepted, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantErr) {
				t.Fatalf("expected an error containing %q, got %q", tt.wantErr, got)
			}
			if tt.wantErr == volumeCleanupLegacyMessage && strings.Contains(got, discardThickOnlyMessage) {
				t.Fatalf("the thick-only rule must not fire on a legacy class, got %q", got)
			}
		})
	}
}

// Test_RSCVolumeCleanupCEL_Update checks that the method stays mutable (it applies to volumes
// created after the change) and that the thick-only rule holds against the new storage type.
func Test_RSCVolumeCleanupCEL_Update(t *testing.T) {
	validator, specStructural := loadRSCSpecCELValidator(t)

	tests := []struct {
		name     string
		old, new srv.ReplicatedStorageClassSpec
		wantErr  string
	}{
		{
			name: "setting a method is accepted",
			old:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVM, ""),
			new:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVM, srv.VolumeCleanupRandomFillSinglePass),
		},
		{
			name: "changing the method is accepted",
			old:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVM, srv.VolumeCleanupRandomFillSinglePass),
			new:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVM, srv.VolumeCleanupDiscard),
		},
		{
			name: "removing the method is accepted",
			old:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVMThin, srv.VolumeCleanupRandomFillThreePass),
			new:  volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVMThin, ""),
		},
		{
			name:    "moving a Discard class to thin storage is rejected",
			old:     volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVM, srv.VolumeCleanupDiscard),
			new:     volumeCleanupSpec(srv.ReplicatedStoragePoolTypeLVMThin, srv.VolumeCleanupDiscard),
			wantErr: discardThickOnlyMessage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs, _ := validator.Validate(context.Background(), field.NewPath("spec"), specStructural,
				specToUnstructured(t, tt.new), specToUnstructured(t, tt.old), celconfig.RuntimeCELCostBudget)
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected the update to be accepted, got %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs.ToAggregate().Error(), tt.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantErr, errs)
			}
		})
	}
}
