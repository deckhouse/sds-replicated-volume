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

package rvcontroller

import (
	"context"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// generatedRVRCRDPath points at the CRD produced by hack/generate_code.sh. It lives at the
// repository root, outside every Go module, so it cannot be embedded with go:embed; the test
// reads it at runtime (the sanctioned exception in go-tests.mdc).
const generatedRVRCRDPath = "../../../../../crds/storage.deckhouse.io_replicatedvolumereplicas.yaml"

// loadRVRSpecCELValidator loads the generated ReplicatedVolumeReplica CRD, builds the structural
// schema for .spec and returns a CEL validator for the spec-level rules — the very rules the
// apiserver runs on every write to an RVR. Building the validator compiles each
// x-kubernetes-validations rule against the per-expression cost budget, so an over-budget rule
// surfaces here instead of only at CRD install time.
func loadRVRSpecCELValidator() (*schemacel.Validator, *structuralschema.Structural) {
	GinkgoHelper()

	data, err := os.ReadFile(generatedRVRCRDPath)
	Expect(err).NotTo(HaveOccurred(), "read generated CRD %q", generatedRVRCRDPath)

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

	specStructural, ok := structural.Properties["spec"]
	Expect(ok).To(BeTrue(), "structural schema has no spec property")

	// isResourceRoot=false: .spec is not an embedded resource (no apiVersion/kind/metadata).
	validator := schemacel.NewValidator(&specStructural, false, celconfig.PerCallLimit)
	Expect(validator).NotTo(BeNil(), "expected non-nil CEL validator for spec (spec has x-kubernetes-validations)")

	return validator, &specStructural
}

// validateRVRSpecCEL runs the generated spec-level CEL rules over a spec write: oldSpec == nil
// validates a creation (transition rules referencing oldSelf are skipped), otherwise it validates
// the oldSpec → newSpec update the way the apiserver would.
//
// Conversion goes through runtime.DefaultUnstructuredConverter (not json.Marshal+Unmarshal) so
// integer fields become int64 rather than float64, matching how the apiserver represents a
// decoded custom resource.
func validateRVRSpecCEL(
	ctx context.Context,
	validator *schemacel.Validator,
	specStructural *structuralschema.Structural,
	oldSpec, newSpec *v1alpha1.ReplicatedVolumeReplicaSpec,
) field.ErrorList {
	GinkgoHelper()

	toUnstructured := func(spec *v1alpha1.ReplicatedVolumeReplicaSpec) map[string]interface{} {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec)
		Expect(err).NotTo(HaveOccurred(), "convert spec to unstructured")
		return m
	}

	var oldObj interface{}
	if oldSpec != nil {
		oldObj = toUnstructured(oldSpec)
	}

	errs, _ := validator.Validate(
		ctx, field.NewPath("spec"), specStructural, toUnstructured(newSpec), oldObj, celconfig.RuntimeCELCostBudget)

	return errs
}

var _ = Describe("reconcileLayoutConvergence: CEL validity of the retype patch", func() {
	var (
		scheme         *runtime.Scheme
		validator      *schemacel.Validator
		specStructural *structuralschema.Structural
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		validator, specStructural = loadRVRSpecCELValidator()
	})

	// Schema controls: prove the harness really enforces the generated spec rules, so an empty
	// error list in the retype test below is evidence and not a silent no-op.
	It("control: accepts a node-only TieBreaker spec", func(ctx SpecContext) {
		errs := validateRVRSpecCEL(ctx, validator, specStructural, nil, &v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: "rv-1",
			NodeName:             "node-2",
			Type:                 v1alpha1.ReplicaTypeTieBreaker,
		})
		Expect(errs).To(BeEmpty())
	})

	It("control: rejects a TieBreaker spec that still carries lvmVolumeGroupName", func(ctx SpecContext) {
		errs := validateRVRSpecCEL(ctx, validator, specStructural, nil, &v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName:       "rv-1",
			NodeName:                   "node-2",
			LVMVolumeGroupName:         "vg-1",
			LVMVolumeGroupThinPoolName: "thin-1",
			Type:                       v1alpha1.ReplicaTypeTieBreaker,
		})
		Expect(errs.ToAggregate()).To(MatchError(ContainSubstring("lvmVolumeGroupName can only be set for Diskful type")))
	})

	It("produces a CEL-valid spec when retyping a scheduled Diskful replica to TieBreaker", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 3, 0) // r2 config (2D+1TB), actual 3D
		// A scheduled Diskful replica always carries its backing-volume fields; the retype must
		// not leave them behind (the apiserver rejects them on a non-Diskful replica).
		for _, r := range rvrs {
			r.Spec.LVMVolumeGroupName = "vg-1"
			r.Spec.LVMVolumeGroupThinPoolName = "thin-1"
		}

		objs := make([]client.Object, 0, len(rvrs))
		for _, r := range rvrs {
			objs = append(objs, r)
		}
		rec := NewReconciler(newClientBuilder(scheme).WithObjects(objs...).Build(), scheme)
		fresh, err := rec.getRVRsSorted(ctx, "rv-1")
		Expect(err).NotTo(HaveOccurred())

		// The lexicographically last unattached Diskful replica is the retype candidate.
		key := client.ObjectKey{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)}
		var before v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, key, &before)).To(Succeed())

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeTrue()) // DoneAndRequeue

		var after v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, key, &after)).To(Succeed())
		Expect(after.Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))

		// The apiserver runs exactly these rules on the retype patch: any error here means the
		// patch is rejected in a real cluster and the r3→r2 migration never progresses.
		Expect(validateRVRSpecCEL(ctx, validator, specStructural, &before.Spec, &after.Spec)).To(BeEmpty())

		// Spelled out: the backing-volume fields are cleared in the same patch as the type flip,
		// and the future tie-breaker keeps its node (spec.nodeName is immutable once set).
		Expect(after.Spec.LVMVolumeGroupName).To(BeEmpty())
		Expect(after.Spec.LVMVolumeGroupThinPoolName).To(BeEmpty())
		Expect(after.Spec.NodeName).To(Equal(before.Spec.NodeName))
	})
})
