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
// ReplicatedStorageClass CRD, not the webhook handlers of this package. It lives here only
// because the CRD sits in crds/ at the repository root, outside every Go module, so no
// package can go:embed it and the test reads it from disk by a relative path; this package
// was chosen as the home of the RSC CEL tests since the RSC webhook enforces the rest of the
// RSC admission rules.
//
// The tests validate the generated CRD, so run hack/generate_code.sh after changing the
// kubebuilder markers in api/v1alpha1/rsc_types.go.

package handlers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// generatedRSCCRDPath points at the CRD produced by hack/generate_code.sh. It lives at the
// repository root, outside every Go module, so it cannot be embedded with go:embed; the test
// reads it at runtime (the sanctioned exception in go-tests.mdc).
const generatedRSCCRDPath = "../../../crds/storage.deckhouse.io_replicatedstorageclasses.yaml"

// loadRSCSpecCELValidator loads the generated ReplicatedStorageClass CRD, builds the
// structural schema for .spec and returns a CEL validator for the spec-level rules. Building
// the validator compiles every x-kubernetes-validations rule (including the immutability
// transition rules) against the per-expression cost budget, so a rule that exceeded the
// budget would surface here (or as an error from Validate) rather than only at CRD install
// time in a real apiserver.
func loadRSCSpecCELValidator(t *testing.T) (*schemacel.Validator, *structuralschema.Structural) {
	t.Helper()

	data, err := os.ReadFile(generatedRSCCRDPath)
	if err != nil {
		t.Fatalf("read generated CRD %q: %v", generatedRSCCRDPath, err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("unmarshal CRD: %v", err)
	}

	var v1Schema *apiextensionsv1.JSONSchemaProps
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Schema != nil && crd.Spec.Versions[i].Schema.OpenAPIV3Schema != nil {
			v1Schema = crd.Spec.Versions[i].Schema.OpenAPIV3Schema
			break
		}
	}
	if v1Schema == nil {
		t.Fatal("CRD has no openAPIV3Schema")
	}

	var internalSchema apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v1Schema, &internalSchema, nil); err != nil {
		t.Fatalf("convert schema to internal: %v", err)
	}

	structural, err := structuralschema.NewStructural(&internalSchema)
	if err != nil {
		t.Fatalf("build structural schema: %v", err)
	}

	specStructural, ok := structural.Properties["spec"]
	if !ok {
		t.Fatal("structural schema has no spec property")
	}

	// isResourceRoot=false: .spec is not an embedded resource (no apiVersion/kind/metadata).
	validator := schemacel.NewValidator(&specStructural, false, celconfig.PerCallLimit)
	if validator == nil {
		t.Fatal("expected non-nil CEL validator for spec (spec has x-kubernetes-validations)")
	}

	return validator, &specStructural
}

// specToUnstructured renders a typed spec as the map form the CEL validator expects. It uses
// the runtime converter (not json.Marshal+Unmarshal) so integer fields become int64 rather
// than float64, matching how the apiserver represents a decoded custom resource — otherwise
// the integer CEL rules fail with "expected int, got float64".
func specToUnstructured(t *testing.T, spec srv.ReplicatedStorageClassSpec) map[string]interface{} {
	t.Helper()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	if err != nil {
		t.Fatalf("convert spec to unstructured: %v", err)
	}
	return m
}

// Test_RSCSpecImmutabilityCEL exercises the CEL transition rules against the *generated* CRD:
// each immutable scalar/bounded field rejects a change (add/remove/modify) with a message
// naming the field, no-op updates and legitimate mutable-field edits pass, and building the
// validator proves all spec rules compile within the per-expression cost budget.
func Test_RSCSpecImmutabilityCEL(t *testing.T) {
	validator, specStructural := loadRSCSpecCELValidator(t)
	ctx := context.Background()

	// base is valid against all pre-existing (create-time) spec rules.
	base := func() srv.ReplicatedStorageClassSpec {
		return srv.ReplicatedStorageClassSpec{
			ReclaimPolicy: srv.RSCReclaimPolicyDelete,
			Topology:      srv.TopologyIgnored,
		}
	}
	withVolumeAccess := func(v srv.ReplicatedStorageClassVolumeAccess) srv.ReplicatedStorageClassSpec {
		s := base()
		s.VolumeAccess = v
		return s
	}
	// newCPStorage returns a spec.storage value; its presence marks a new control plane class.
	newCPStorage := func() *srv.ReplicatedStorageClassStorage {
		return &srv.ReplicatedStorageClassStorage{
			Type:            srv.ReplicatedStoragePoolType("LVM"),
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a"}},
		}
	}
	// withZones returns a TransZonal spec over the given zones; a nil storage makes it a legacy
	// control plane class.
	withZones := func(storage *srv.ReplicatedStorageClassStorage, zones ...string) srv.ReplicatedStorageClassSpec {
		s := base()
		s.Storage = storage
		s.Topology = srv.TopologyTransZonal
		s.Zones = zones
		return s
	}

	tests := []struct {
		name string
		old  srv.ReplicatedStorageClassSpec
		new  srv.ReplicatedStorageClassSpec
		// wantErrSubstring empty means the update must be accepted (no CEL errors).
		wantErrSubstring string
	}{
		{
			name: "no-op update is accepted",
			old:  base(),
			new:  base(),
		},
		{
			name: "reclaimPolicy change is accepted for the new control plane (the StorageClass is recreated with the new policy)",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = newCPStorage()
				s.ReclaimPolicy = srv.RSCReclaimPolicyDelete
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = newCPStorage()
				s.ReclaimPolicy = srv.RSCReclaimPolicyRetain
				return s
			}(),
		},
		{
			name: "reclaimPolicy change is rejected for the legacy control plane",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.ReclaimPolicy = srv.RSCReclaimPolicyDelete
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.ReclaimPolicy = srv.RSCReclaimPolicyRetain
				return s
			}(),
			wantErrSubstring: "spec.reclaimPolicy is immutable for the legacy control plane",
		},
		{
			name:             "topology change is rejected",
			old:              func() srv.ReplicatedStorageClassSpec { s := base(); s.Topology = srv.TopologyIgnored; return s }(),
			new:              func() srv.ReplicatedStorageClassSpec { s := base(); s.Topology = srv.TopologyZonal; return s }(),
			wantErrSubstring: "spec.topology is immutable",
		},
		{
			name:             "volumeAccess change is rejected",
			old:              withVolumeAccess(srv.VolumeAccessPreferablyLocal),
			new:              withVolumeAccess(srv.VolumeAccessAny),
			wantErrSubstring: "spec.volumeAccess is immutable",
		},
		{
			name:             "volumeAccess added is rejected",
			old:              base(),
			new:              withVolumeAccess(srv.VolumeAccessAny),
			wantErrSubstring: "spec.volumeAccess is immutable",
		},
		{
			// The new control plane applies a zones change through a new auto-generated
			// storage pool.
			name: "zones change is accepted for the new control plane",
			old:  withZones(newCPStorage(), "zone-a", "zone-b", "zone-c"),
			new:  withZones(newCPStorage(), "zone-a", "zone-b", "zone-d"),
		},
		{
			name:             "zones change is rejected for the legacy control plane",
			old:              withZones(nil, "zone-a", "zone-b", "zone-c"),
			new:              withZones(nil, "zone-a", "zone-b", "zone-d"),
			wantErrSubstring: "spec.zones is immutable for the legacy control plane",
		},
		{
			name: "zones added is rejected for the legacy control plane",
			old:  base(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Zones = []string{"zone-a"}
				return s
			}(),
			wantErrSubstring: "spec.zones is immutable for the legacy control plane",
		},
		{
			// zones is a set: reordering it is not a change.
			name: "zones reordered is accepted for the legacy control plane",
			old:  withZones(nil, "zone-a", "zone-b", "zone-c"),
			new:  withZones(nil, "zone-c", "zone-a", "zone-b"),
		},
		{
			name: "zones no-op (same set) is accepted for the legacy control plane",
			old:  withZones(nil, "zone-a", "zone-b", "zone-c"),
			new:  withZones(nil, "zone-a", "zone-b", "zone-c"),
		},
		{
			// A class counts as a new control plane one only if spec.storage is set both
			// before and after the update: adding it in the same request does not lift the
			// legacy restriction.
			name:             "zones change together with adding storage is rejected for the legacy control plane",
			old:              withZones(nil, "zone-a", "zone-b", "zone-c"),
			new:              withZones(newCPStorage(), "zone-a", "zone-b", "zone-d"),
			wantErrSubstring: "spec.zones is immutable for the legacy control plane",
		},
		{
			name:             "zones change together with removing storage is rejected",
			old:              withZones(newCPStorage(), "zone-a", "zone-b", "zone-c"),
			new:              withZones(nil, "zone-a", "zone-b", "zone-d"),
			wantErrSubstring: "spec.zones is immutable for the legacy control plane",
		},
		{
			name: "reclaimPolicy change together with adding storage is rejected for the legacy control plane",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.ReclaimPolicy = srv.RSCReclaimPolicyDelete
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = newCPStorage()
				s.ReclaimPolicy = srv.RSCReclaimPolicyRetain
				return s
			}(),
			wantErrSubstring: "spec.reclaimPolicy is immutable for the legacy control plane",
		},
		{
			// Regression guard for the controller's storagePool->storage migration: it sets
			// storage from nil and clears storagePool, leaving zones and reclaimPolicy as is.
			name: "storagePool->storage migration (zones and reclaimPolicy unchanged) is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := withZones(nil, "zone-a", "zone-b", "zone-c")
				s.StoragePool = "legacy-pool" //nolint:staticcheck // SA1019: the migration under test clears the deprecated StoragePool field
				return s
			}(),
			new: withZones(newCPStorage(), "zone-a", "zone-b", "zone-c"),
		},
		{
			// systemNetworkNames is immutable once set; the controller fills it from nil, so
			// the nil->value transition must be accepted.
			name: "systemNetworkNames added from nil (controller default) is accepted",
			old:  base(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.SystemNetworkNames = []string{"Internal"}
				return s
			}(),
		},
		{
			name: "systemNetworkNames removed is rejected",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.SystemNetworkNames = []string{"Internal"}
				return s
			}(),
			new:              base(),
			wantErrSubstring: "spec.systemNetworkNames is immutable once set",
		},
		{
			// eligibleNodesPolicy is part of the storage pool identity: a change moves the class
			// to a new pool, so it must be accepted.
			name: "eligibleNodesPolicy change is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesPolicy = &srv.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute}}
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesPolicy = &srv.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 5 * time.Minute}}
				return s
			}(),
		},
		{
			// Removing the policy makes the controller restore the default, which is an
			// ordinary policy change.
			name: "eligibleNodesPolicy removed is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesPolicy = &srv.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute}}
				return s
			}(),
			new: base(),
		},
		{
			// The controller fills eligibleNodesPolicy from nil, so the nil->value transition
			// must be accepted.
			name: "eligibleNodesPolicy added from nil (controller default) is accepted",
			old:  base(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesPolicy = &srv.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute}}
				return s
			}(),
		},
		{
			// Regression guard for the controller's applySpecDefaults: it fills all four
			// controller-managed optional fields from nil in a single patch. None of them may
			// be rejected by the transition rules.
			name: "controller applySpecDefaults (all managed fields nil->value) is accepted",
			old:  base(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.SystemNetworkNames = []string{"Internal"}
				s.ConfigurationRolloutStrategy = &srv.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type:          srv.ConfigurationRolloutRollingUpdate,
					RollingUpdate: &srv.ReplicatedStorageClassConfigurationRollingUpdateStrategy{MaxParallel: 5},
				}
				s.EligibleNodesConflictResolutionStrategy = &srv.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type:          srv.EligibleNodesConflictResolutionRollingRepair,
					RollingRepair: &srv.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{MaxParallel: 5},
				}
				s.EligibleNodesPolicy = &srv.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute}}
				return s
			}(),
		},
		{
			// Replication is the field the r3->r2 migration procedure edits, so it must stay
			// mutable. Built via composite literal (not a selector assignment) to match how the
			// deprecated Replication field is used elsewhere in the repo.
			name: "changing replication (mutable) is accepted",
			old:  srv.ReplicatedStorageClassSpec{ReclaimPolicy: srv.RSCReclaimPolicyDelete, Topology: srv.TopologyIgnored, Replication: srv.ReplicationConsistencyAndAvailability}, //nolint:staticcheck // SA1019: the CEL rule under test guards the deprecated Replication field
			new:  srv.ReplicatedStorageClassSpec{ReclaimPolicy: srv.RSCReclaimPolicyDelete, Topology: srv.TopologyIgnored, Replication: srv.ReplicationAvailability},               //nolint:staticcheck // SA1019: the CEL rule under test guards the deprecated Replication field
		},
		{
			// FTT/GMDR are the primary r3->r2 migration knobs and must stay mutable; the CEL
			// rules must not block them. FTT=1/GMDR=1 -> FTT=1/GMDR=0 is a valid transition.
			name: "changing failuresToTolerate/guaranteedMinimumDataRedundancy (mutable) is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.FailuresToTolerate = bytePtr(1)
				s.GuaranteedMinimumDataRedundancy = bytePtr(1)
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.FailuresToTolerate = bytePtr(1)
				s.GuaranteedMinimumDataRedundancy = bytePtr(0)
				return s
			}(),
		},
		{
			name: "changing configurationRolloutStrategy (mutable) is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.ConfigurationRolloutStrategy = &srv.ReplicatedStorageClassConfigurationRolloutStrategy{Type: srv.ConfigurationRolloutNewVolumesOnly}
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.ConfigurationRolloutStrategy = &srv.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type:          srv.ConfigurationRolloutRollingUpdate,
					RollingUpdate: &srv.ReplicatedStorageClassConfigurationRollingUpdateStrategy{MaxParallel: 5},
				}
				return s
			}(),
		},
		{
			name: "changing eligibleNodesConflictResolutionStrategy (mutable) is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesConflictResolutionStrategy = &srv.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: srv.EligibleNodesConflictResolutionManual}
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.EligibleNodesConflictResolutionStrategy = &srv.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type:          srv.EligibleNodesConflictResolutionRollingRepair,
					RollingRepair: &srv.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{MaxParallel: 5},
				}
				return s
			}(),
		},
		{
			// storage is mutable: the controller moves the class to a new auto-generated
			// storage pool built from the new value.
			name: "storage change is accepted",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = &srv.ReplicatedStorageClassStorage{
					Type:            srv.ReplicatedStoragePoolType("LVM"),
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a"}},
				}
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = &srv.ReplicatedStorageClassStorage{
					Type:            srv.ReplicatedStoragePoolType("LVM"),
					LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-b"}},
				}
				return s
			}(),
		},
		{
			// nodeLabelSelector is mutable for the same reason as storage.
			name: "nodeLabelSelector change is accepted for the new control plane",
			old: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = newCPStorage()
				s.NodeLabelSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"role": "storage"}}
				return s
			}(),
			new: func() srv.ReplicatedStorageClassSpec {
				s := base()
				s.Storage = newCPStorage()
				s.NodeLabelSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"role": "compute"}}
				return s
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := specToUnstructured(t, tt.old)
			newObj := specToUnstructured(t, tt.new)

			errs, _ := validator.Validate(ctx, field.NewPath("spec"), specStructural, newObj, oldObj, celconfig.RuntimeCELCostBudget)

			if tt.wantErrSubstring == "" {
				if len(errs) != 0 {
					t.Fatalf("expected update to be accepted, got errors: %v", errs)
				}
				return
			}

			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tt.wantErrSubstring) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected an error containing %q, got: %v", tt.wantErrSubstring, errs)
			}
		})
	}
}
