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

// This file pins the TransZonal zone-count rules of the generated ReplicatedStorageClass
// CRD to isTransZonalZoneCountValid of this package: it loads the CRD from disk (crds/ sits
// at the repository root, outside every Go module, so it cannot be go:embed-ed) and checks
// that for every admitted FTT/GMDR/zone-count combination the admission CEL rules accept
// exactly what the runtime check accepts. It also pins the admission cap itself: FTT and
// GMDR are limited to 1 by the field markers (layouts above FTT=1/GMDR=1 exist in the
// datamesh but are not tested in DRBD), in both the RSC spec and the RV
// spec.manualConfiguration, so a marker bump cannot silently widen admission.
//
// The zone matrix lives in two more places. The original swap bug (FTT=1/GMDR=2 and
// FTT=2/GMDR=1) slipped through precisely because each copy was pinned by its own test
// alone, so every copy names its pin: the CEL markers and isTransZonalZoneCountValid —
// the two admission-critical copies whose divergence breaks volumes — are cross-checked
// here mechanically, the same way reconciler_retype_cel_test.go pins the RVR CEL rules
// to this package's retype patches; the third copy, computeTransZonalMinZones in
// rsc_controller, is not importable from this package (internal package boundary), so it
// is pinned by its own unit tests (the validateEligibleNodes cases, including the
// unsupported-combination one) in the rsc_controller package.
//
// The test validates the generated CRD, so run hack/generate_code.sh after changing the
// kubebuilder markers in api/v1alpha1/rsc_types.go.

package rvcontroller

import (
	"context"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// generatedRSCCRDPath points at the CRD produced by hack/generate_code.sh. The RSC CRD
// stays at the top of crds/ (it is part of the public API surface), outside every Go
// module, so it cannot be embedded with go:embed; the test reads it at runtime (the
// sanctioned exception in go-tests.mdc).
const generatedRSCCRDPath = "../../../../../crds/storage.deckhouse.io_replicatedstorageclasses.yaml"

// generatedRVCRDPath points at the generated ReplicatedVolume CRD (kept in
// crds/newcontrolplane/, see hack/generate_crds.sh), read at runtime for the same reason.
const generatedRVCRDPath = "../../../../../crds/newcontrolplane/storage.deckhouse.io_replicatedvolumes.yaml"

// loadCRDStructural loads a generated CRD and returns the structural schema of its whole
// document (the .spec properties live under Properties["spec"]).
func loadCRDStructural(crdPath string) *structuralschema.Structural {
	GinkgoHelper()

	data, err := os.ReadFile(crdPath)
	Expect(err).NotTo(HaveOccurred(), "read generated CRD %q", crdPath)

	var crd apiextensionsv1.CustomResourceDefinition
	Expect(yaml.Unmarshal(data, &crd)).To(Succeed(), "unmarshal CRD")

	var v1Schema *apiextensionsv1.JSONSchemaProps
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Schema != nil && crd.Spec.Versions[i].Schema.OpenAPIV3Schema != nil {
			v1Schema = crd.Spec.Versions[i].Schema.OpenAPIV3Schema
			break
		}
	}
	Expect(v1Schema).NotTo(BeNil(), "CRD has no openAPIV3Schema")

	var internalSchema apiextensions.JSONSchemaProps
	Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v1Schema, &internalSchema, nil)).
		To(Succeed(), "convert schema to internal")

	structural, err := structuralschema.NewStructural(&internalSchema)
	Expect(err).NotTo(HaveOccurred(), "build structural schema")

	return structural
}

// loadRSCSpecCELValidator loads the generated ReplicatedStorageClass CRD, builds the
// structural schema for .spec and returns a CEL validator for the spec-level rules — the
// same rules the apiserver runs on every RSC admission.
func loadRSCSpecCELValidator() (*schemacel.Validator, *structuralschema.Structural) {
	GinkgoHelper()

	specStructural, ok := loadCRDStructural(generatedRSCCRDPath).Properties["spec"]
	Expect(ok).To(BeTrue(), "structural schema has no spec property")

	// isResourceRoot=false: .spec is not an embedded resource (no apiVersion/kind/metadata).
	validator := schemacel.NewValidator(&specStructural, false, celconfig.PerCallLimit)
	Expect(validator).NotTo(BeNil(), "expected non-nil CEL validator for spec (spec has x-kubernetes-validations)")

	return validator, &specStructural
}

// validateRSCSpecCELCreate runs the generated spec-level CEL rules over a creation of an
// RSC with the given spec (transition rules referencing oldSelf are skipped). Conversion
// goes through runtime.DefaultUnstructuredConverter so integer fields become int64,
// matching how the apiserver represents a decoded custom resource.
func validateRSCSpecCELCreate(
	ctx context.Context,
	validator *schemacel.Validator,
	specStructural *structuralschema.Structural,
	spec *v1alpha1.ReplicatedStorageClassSpec,
) error {
	GinkgoHelper()

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec)
	Expect(err).NotTo(HaveOccurred(), "convert spec to unstructured")

	errs, _ := validator.Validate(ctx, field.NewPath("spec"), specStructural, obj, nil, celconfig.RuntimeCELCostBudget)

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", errs.ToAggregate().Error())
}

var _ = Describe("TransZonal zone counts: RSC CEL rules vs isTransZonalZoneCountValid", func() {
	var (
		validator      *schemacel.Validator
		specStructural *structuralschema.Structural
	)

	BeforeEach(func() {
		validator, specStructural = loadRSCSpecCELValidator()
	})

	It("admission accepts exactly the zone counts the runtime check accepts", func() {
		// Admission caps FTT/GMDR at 1 (structural Maximum markers; pinned by the It below),
		// so the equivalence grid only covers the admitted domain — the CEL harness runs
		// x-kubernetes-validations alone and does not enforce structural constraints.
		for ftt := byte(0); ftt <= 1; ftt++ {
			for gmdr := byte(0); gmdr <= 1; gmdr++ {
				for zoneCount := 0; zoneCount <= 10; zoneCount++ {
					// New control plane spec: storage set, explicit FTT/GMDR, no replication.
					zones := make([]string, zoneCount)
					for i := range zones {
						zones[i] = fmt.Sprintf("zone-%d", i)
					}
					spec := &v1alpha1.ReplicatedStorageClassSpec{
						Storage: &v1alpha1.ReplicatedStorageClassStorage{
							Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
							// A named group keeps the storage sub-schema CEL rules happy
							// (they reference lvmVolumeGroups without has() guards).
							LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "vg"}},
						},
						Topology: v1alpha1.TopologyTransZonal,
						Zones:    zones,
					}
					spec.FailuresToTolerate = &ftt
					spec.GuaranteedMinimumDataRedundancy = &gmdr

					celErr := validateRSCSpecCELCreate(context.Background(), validator, specStructural, spec)
					goValid := isTransZonalZoneCountValid(ftt, gmdr, zoneCount)

					if goValid {
						Expect(celErr).NotTo(HaveOccurred(),
							"FTT=%d, GMDR=%d, zones=%d: isTransZonalZoneCountValid accepts, but CEL rejects",
							ftt, gmdr, zoneCount)
						continue
					}
					Expect(celErr).To(HaveOccurred(),
						"FTT=%d, GMDR=%d, zones=%d: isTransZonalZoneCountValid rejects, but CEL accepts",
						ftt, gmdr, zoneCount)
				}
			}
		}
	})

	It("the admission schema caps FTT and GMDR at 1 in the RSC and RV CRDs", func() {
		// The datamesh has layouts above FTT=1/GMDR=1, but they are not tested in DRBD, so
		// admission is limited to the LINSTOR-parity layouts (plus Consistency). The cap lives
		// in the field markers only; this pins the generated schema so a marker bump does not
		// silently widen admission.
		rscStructural := loadCRDStructural(generatedRSCCRDPath)
		for _, field := range []string{"failuresToTolerate", "guaranteedMinimumDataRedundancy"} {
			Expect(rscStructural.Properties["spec"].Properties[field].ValueValidation).NotTo(BeNil(),
				"RSC spec.%s must carry value validation", field)
			Expect(rscStructural.Properties["spec"].Properties[field].ValueValidation.Maximum).To(Equal(ptr.To(1.0)),
				"RSC spec.%s must be capped at 1", field)
		}

		rvStructural := loadCRDStructural(generatedRVCRDPath)
		manual := rvStructural.Properties["spec"].Properties["manualConfiguration"]
		Expect(manual.Properties).To(HaveKey("failuresToTolerate"),
			"RV spec.manualConfiguration must carry the FTT field")
		for _, field := range []string{"failuresToTolerate", "guaranteedMinimumDataRedundancy"} {
			Expect(manual.Properties[field].ValueValidation).NotTo(BeNil(),
				"RV spec.manualConfiguration.%s must carry value validation", field)
			Expect(manual.Properties[field].ValueValidation.Maximum).To(Equal(ptr.To(1.0)),
				"RV spec.manualConfiguration.%s must be capped at 1", field)
		}
	})
})
