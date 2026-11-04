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

// This file runs the CEL rule of the generated ReplicatedVolume CRD that fixes
// spec.volumeCleanup when the volume is created; see reconciler_zone_cel_consistency_test.go
// for how the generated CRDs are read.

package rvcontroller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("ReplicatedVolume spec.volumeCleanup CEL rules", func() {
	const immutableMessage = "spec.volumeCleanup is immutable"

	var validate func(newMethod v1alpha1.VolumeCleanupMethod, oldMethod *v1alpha1.VolumeCleanupMethod) string

	BeforeEach(func() {
		specStructural, ok := loadCRDStructural(generatedRVCRDPath).Properties["spec"]
		Expect(ok).To(BeTrue(), "structural schema has no spec property")
		validator := schemacel.NewValidator(&specStructural, false, celconfig.PerCallLimit)
		Expect(validator).NotTo(BeNil())

		toUnstructured := func(method v1alpha1.VolumeCleanupMethod) map[string]interface{} {
			spec := v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("1Gi"),
				ConfigurationMode:          v1alpha1.ReplicatedVolumeConfigurationModeAuto,
				ReplicatedStorageClassName: "rsc-1",
				VolumeCleanup:              method,
			}
			obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
			Expect(err).NotTo(HaveOccurred())
			return obj
		}

		validate = func(newMethod v1alpha1.VolumeCleanupMethod, oldMethod *v1alpha1.VolumeCleanupMethod) string {
			var oldObj interface{}
			if oldMethod != nil {
				oldObj = toUnstructured(*oldMethod)
			}
			errs, _ := validator.Validate(context.Background(), field.NewPath("spec"), &specStructural,
				toUnstructured(newMethod), oldObj, celconfig.RuntimeCELCostBudget)
			if len(errs) == 0 {
				return ""
			}
			return errs.ToAggregate().Error()
		}
	})

	method := func(m v1alpha1.VolumeCleanupMethod) *v1alpha1.VolumeCleanupMethod { return &m }

	It("accepts any method when the volume is created", func() {
		for _, m := range []v1alpha1.VolumeCleanupMethod{"",
			v1alpha1.VolumeCleanupRandomFillSinglePass,
			v1alpha1.VolumeCleanupRandomFillThreePass,
			v1alpha1.VolumeCleanupDiscard,
		} {
			Expect(validate(m, nil)).To(BeEmpty(), "method %q", m)
		}
	})

	It("accepts an update that keeps the method", func() {
		Expect(validate(v1alpha1.VolumeCleanupDiscard, method(v1alpha1.VolumeCleanupDiscard))).To(BeEmpty())
		Expect(validate("", method(""))).To(BeEmpty())
	})

	It("rejects setting, changing and removing the method on an existing volume", func() {
		Expect(validate(v1alpha1.VolumeCleanupRandomFillSinglePass, method(""))).To(ContainSubstring(immutableMessage))
		Expect(validate(v1alpha1.VolumeCleanupRandomFillThreePass, method(v1alpha1.VolumeCleanupRandomFillSinglePass))).
			To(ContainSubstring(immutableMessage))
		Expect(validate("", method(v1alpha1.VolumeCleanupDiscard))).To(ContainSubstring(immutableMessage))
	})
})
