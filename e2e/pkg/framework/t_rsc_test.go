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

package framework

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	tk "github.com/deckhouse/sds-replicated-volume/lib/go/testkit"
)

func newTestRSCForTest() *TestRSC {
	return &TestRSC{
		TrackedObject: tk.NewTrackedObject(nil, nil, schema.GroupVersionKind{}, "test-rsc", tk.Lifecycle[*v1alpha1.ReplicatedStorageClass]{}),
	}
}

var _ = Describe("TestRSC builder", func() {
	It("ConfigurationRolloutStrategyType sets type only", func() {
		trsc := newTestRSCForTest().
			ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutNewVolumesOnly)
		rsc := trsc.buildObject()
		Expect(rsc.Spec.ConfigurationRolloutStrategy).NotTo(BeNil())
		Expect(rsc.Spec.ConfigurationRolloutStrategy.Type).To(Equal(v1alpha1.ConfigurationRolloutNewVolumesOnly))
		Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate).To(BeNil())
	})

	It("ConfigurationRolloutStrategyType with MaxParallel sets both", func() {
		trsc := newTestRSCForTest().
			ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutRollingUpdate).
			ConfigurationRolloutStrategyMaxParallel(10)
		rsc := trsc.buildObject()
		Expect(rsc.Spec.ConfigurationRolloutStrategy).NotTo(BeNil())
		Expect(rsc.Spec.ConfigurationRolloutStrategy.Type).To(Equal(v1alpha1.ConfigurationRolloutRollingUpdate))
		Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate).NotTo(BeNil())
		Expect(rsc.Spec.ConfigurationRolloutStrategy.RollingUpdate.MaxParallel).To(Equal(int32(10)))
	})

	It("EligibleNodesConflictResolutionStrategyType sets type only", func() {
		trsc := newTestRSCForTest().
			EligibleNodesConflictResolutionStrategyType(v1alpha1.EligibleNodesConflictResolutionManual)
		rsc := trsc.buildObject()
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy).NotTo(BeNil())
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.Type).To(Equal(v1alpha1.EligibleNodesConflictResolutionManual))
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair).To(BeNil())
	})

	It("EligibleNodesConflictResolutionStrategyType with MaxParallel sets both", func() {
		trsc := newTestRSCForTest().
			EligibleNodesConflictResolutionStrategyType(v1alpha1.EligibleNodesConflictResolutionRollingRepair).
			EligibleNodesConflictResolutionStrategyMaxParallel(20)
		rsc := trsc.buildObject()
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy).NotTo(BeNil())
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.Type).To(Equal(v1alpha1.EligibleNodesConflictResolutionRollingRepair))
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair).NotTo(BeNil())
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy.RollingRepair.MaxParallel).To(Equal(int32(20)))
	})

	It("unset strategies leave spec fields nil", func() {
		trsc := newTestRSCForTest()
		rsc := trsc.buildObject()
		Expect(rsc.Spec.ConfigurationRolloutStrategy).To(BeNil())
		Expect(rsc.Spec.EligibleNodesConflictResolutionStrategy).To(BeNil())
	})

	It("fluent chain returns same instance", func() {
		trsc := newTestRSCForTest()
		same := trsc.
			ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutRollingUpdate).
			EligibleNodesConflictResolutionStrategyType(v1alpha1.EligibleNodesConflictResolutionManual)
		Expect(same).To(BeIdenticalTo(trsc))
	})

	It("CreateExpect is no-op with nil client", func() {
		trsc := newTestRSCForTest()
		trsc.CreateExpect(context.Background(), Succeed())
		Expect(trsc.Name()).To(Equal("test-rsc"))
	})

	It("GetExpect is no-op with nil client", func() {
		trsc := newTestRSCForTest()
		trsc.GetExpect(context.Background(), Succeed())
		Expect(trsc.Name()).To(Equal("test-rsc"))
	})

	It("Get is no-op with nil client", func() {
		trsc := newTestRSCForTest()
		trsc.Get(context.Background())
		Expect(trsc.Name()).To(Equal("test-rsc"))
	})
})
