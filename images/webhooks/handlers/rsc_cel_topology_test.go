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
	"fmt"
	"slices"
	"strings"
	"testing"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// maxProbedZones bounds the zone counts probed for every configuration: one above the largest
// valid TransZonal count (5), so an "exactly N" rule that silently became "at least N" fails.
const maxProbedZones = 6

// transZonalSpec builds a TransZonal spec with n zones. withStorage selects the control plane
// the CEL rules assume: spec.storage set means the new control plane, unset means the legacy one.
func transZonalSpec(withStorage bool, n int) srv.ReplicatedStorageClassSpec {
	s := srv.ReplicatedStorageClassSpec{
		ReclaimPolicy: srv.RSCReclaimPolicyDelete,
		Topology:      srv.TopologyTransZonal,
	}
	if withStorage {
		s.Storage = &srv.ReplicatedStorageClassStorage{
			Type:            srv.ReplicatedStoragePoolTypeLVM,
			LVMVolumeGroups: []srv.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg-a"}},
		}
	} else {
		s.StoragePool = "legacy-pool" //nolint:staticcheck // legacy control plane RSCs use the deprecated storagePool
	}
	for i := range n {
		s.Zones = append(s.Zones, fmt.Sprintf("zone-%d", i))
	}
	return s
}

// createValidator returns a function that runs the spec-level CEL rules of the generated CRD
// as on create and returns the joined error messages ("" when the spec is accepted). The CRD
// is compiled once per call so that the many sub-tests below share it.
func createValidator(t *testing.T) func(t *testing.T, spec srv.ReplicatedStorageClassSpec) string {
	t.Helper()
	validator, specStructural := loadRSCSpecCELValidator(t)
	return func(t *testing.T, spec srv.ReplicatedStorageClassSpec) string {
		t.Helper()
		return validateCreate(t, validator, specStructural, spec)
	}
}

func validateCreate(
	t *testing.T,
	validator *schemacel.Validator,
	specStructural *structuralschema.Structural,
	spec srv.ReplicatedStorageClassSpec,
) string {
	t.Helper()
	errs, _ := validator.Validate(context.Background(), field.NewPath("spec"), specStructural,
		specToUnstructured(t, spec), nil, celconfig.RuntimeCELCostBudget)
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return strings.Join(msgs, "; ")
}

// Test_RSCTransZonalZonesCEL_NewControlPlane checks that on the new control plane every
// redundancy setting accepts exactly the TransZonal zone counts isTransZonalZoneCountValid
// (rv_controller) accepts, and that an empty zone list is rejected.
func Test_RSCTransZonalZonesCEL_NewControlPlane(t *testing.T) {
	validate := createValidator(t)
	byteptr := func(v byte) *byte { return &v }

	tests := []struct {
		name        string
		replication srv.ReplicatedStorageClassReplication
		ftt, gmdr   *byte
		validZones  []int
	}{
		// Admission caps FTT/GMDR at 1 (structural markers; higher layouts exist in the
		// datamesh but are not tested in DRBD), so the grid only covers the admitted combos.
		// Regression: FTT=0/GMDR=1 was rejected with 2 zones by the legacy default rule.
		{name: "FTT=0 GMDR=1 (2D)", ftt: byteptr(0), gmdr: byteptr(1), validZones: []int{2}},
		{name: "FTT=1 GMDR=0 (2D+1TB)", ftt: byteptr(1), gmdr: byteptr(0), validZones: []int{3}},
		{name: "FTT=1 GMDR=1 (3D)", ftt: byteptr(1), gmdr: byteptr(1), validZones: []int{3}},
		{name: "replication Consistency", replication: srv.ReplicationConsistency, validZones: []int{2}},
		{name: "replication Availability", replication: srv.ReplicationAvailability, validZones: []int{3}},
		{name: "replication ConsistencyAndAvailability", replication: srv.ReplicationConsistencyAndAvailability, validZones: []int{3}},
		{name: "replication unset (ConsistencyAndAvailability default)", validZones: []int{3}},
	}

	for _, tt := range tests {
		for n := 0; n <= maxProbedZones; n++ {
			t.Run(fmt.Sprintf("%s/%d zones", tt.name, n), func(t *testing.T) {
				spec := transZonalSpec(true, n)
				spec.Replication = tt.replication //nolint:staticcheck // the deprecated replication field is part of the validated API
				spec.FailuresToTolerate = tt.ftt
				spec.GuaranteedMinimumDataRedundancy = tt.gmdr

				got := validate(t, spec)

				switch {
				case n == 0:
					// The "size(self.zones) == 0 ||" guards in the exact-count rules must keep the
					// empty list to the single "requires zones to be specified" message.
					if !strings.Contains(got, "TransZonal topology requires zones to be specified") ||
						strings.Contains(got, "requires exactly") {
						t.Fatalf("expected only the empty zone list error, got %q", got)
					}
				case slices.Contains(tt.validZones, n):
					if got != "" {
						t.Fatalf("expected %d zones to be accepted, got %q", n, got)
					}
				default:
					if !strings.Contains(got, "requires exactly") {
						t.Fatalf("expected %d zones to be rejected with an exact zone count message, got %q", n, got)
					}
				}
			})
		}
	}
}

// Test_RSCTransZonalZonesCEL_LegacyControlPlane pins the legacy control plane behavior: only
// lower bounds on the zone count, an empty zone list passes CEL (the legacy webhook rejects
// it instead), and replication Consistency is rejected outright — the legacy control plane
// never supported it (the old controller has no StorageClass layout for that mode).
func Test_RSCTransZonalZonesCEL_LegacyControlPlane(t *testing.T) {
	validate := createValidator(t)
	tests := []struct {
		name        string
		replication srv.ReplicatedStorageClassReplication
		minZones    int
	}{
		{name: "replication Availability", replication: srv.ReplicationAvailability, minZones: 3},
		{name: "replication ConsistencyAndAvailability", replication: srv.ReplicationConsistencyAndAvailability, minZones: 3},
		{name: "replication unset (ConsistencyAndAvailability default)", minZones: 3},
	}

	for _, tt := range tests {
		for n := 0; n <= maxProbedZones; n++ {
			t.Run(fmt.Sprintf("%s/%d zones", tt.name, n), func(t *testing.T) {
				spec := transZonalSpec(false, n)
				spec.Replication = tt.replication //nolint:staticcheck // the deprecated replication field is part of the validated API

				got := validate(t, spec)

				if n == 0 || n >= tt.minZones {
					if got != "" {
						t.Fatalf("expected %d zones to be accepted, got %q", n, got)
					}
					return
				}
				if !strings.Contains(got, fmt.Sprintf("requires at least %d zones", tt.minZones)) {
					t.Fatalf("expected %d zones to be rejected with the legacy lower bound, got %q", n, got)
				}
			})
		}
	}
}

// Test_RSCConsistencyCEL_LegacyControlPlane checks that the legacy control plane rejects
// replication Consistency for every zone count and topology: the old controller has no
// StorageClass layout for it, and the legacy webhook rejects it too.
func Test_RSCConsistencyCEL_LegacyControlPlane(t *testing.T) {
	validate := createValidator(t)
	for _, topology := range []srv.ReplicatedStorageClassTopology{srv.TopologyTransZonal, srv.TopologyZonal, srv.TopologyIgnored} {
		for n := 0; n <= maxProbedZones; n++ {
			t.Run(fmt.Sprintf("%s/%d zones", topology, n), func(t *testing.T) {
				spec := transZonalSpec(false, n)
				spec.Topology = topology
				spec.Replication = srv.ReplicationConsistency //nolint:staticcheck // the deprecated replication field is part of the validated API

				got := validate(t, spec)

				// The ban is topology-independent and must be the only rejection: no legacy
				// lower-bound or new control plane exact-count message may pile onto it.
				if !strings.Contains(got, "Replication Consistency is not supported by the legacy control plane") ||
					strings.Contains(got, "requires at least") ||
					strings.Contains(got, "requires exactly") {
					t.Fatalf("expected only the legacy Consistency rejection, got %q", got)
				}
			})
		}
	}
}

// Test_RSCNonTransZonalZonesCEL_NewControlPlane checks that the new control plane zone rules
// are scoped to TransZonal: Zonal and Ignored accept any zone list (zones act as a node filter).
func Test_RSCNonTransZonalZonesCEL_NewControlPlane(t *testing.T) {
	validate := createValidator(t)
	for _, topology := range []srv.ReplicatedStorageClassTopology{srv.TopologyZonal, srv.TopologyIgnored} {
		for n := 0; n <= maxProbedZones; n++ {
			t.Run(fmt.Sprintf("%s/%d zones", topology, n), func(t *testing.T) {
				spec := transZonalSpec(true, n)
				spec.Topology = topology

				if got := validate(t, spec); got != "" {
					t.Fatalf("expected %s with %d zones to be accepted, got %q", topology, n, got)
				}
			})
		}
	}
}
