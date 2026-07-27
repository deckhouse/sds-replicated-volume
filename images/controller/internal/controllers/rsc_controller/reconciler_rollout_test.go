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

package rsccontroller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Condition-state fixtures shared by the rollout classification tests
//

// condTrue is a condition written for the volume's current generation, reporting True.
func condTrue() rvViewCondition {
	return rvViewCondition{present: true, status: metav1.ConditionTrue, current: true}
}

// condFalse is a condition written for the volume's current generation, reporting False.
func condFalse() rvViewCondition {
	return rvViewCondition{present: true, status: metav1.ConditionFalse, current: true}
}

// condUnknown is a condition written for the volume's current generation, reporting Unknown
// (for example LayoutConverged=Unknown/VolumeDeleting).
func condUnknown() rvViewCondition {
	return rvViewCondition{present: true, status: metav1.ConditionUnknown, current: true}
}

// condStale is a True condition left over from an older volume generation.
func condStale() rvViewCondition {
	return rvViewCondition{present: true, status: metav1.ConditionTrue, current: false}
}

// condMissing is an absent condition.
func condMissing() rvViewCondition {
	return rvViewCondition{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Rollout aggregation: a volume that produced no verdict is pending, never rolled out
//

var _ = Describe("rollout aggregation of volumes without a verdict", func() {
	var rsc *v1alpha1.ReplicatedStorageClass

	BeforeEach(func() {
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ConfigurationRolloutRollingUpdate,
					RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
						MaxParallel: 5,
					},
				},
				EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.EligibleNodesConflictResolutionManual,
				},
			},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration:          1,
				StoragePoolEligibleNodesRevision: 1,
			},
		}
	})

	It("counts a brand-new volume as pending, not as silently aligned", func() {
		// A volume created a moment ago: it has no conditions and has not recorded which
		// configuration generation it observed. Absence of data is not a completion signal.
		rvs := []rvView{{name: "rv-1"}}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(*counters.PendingObservation).To(Equal(int32(1)))
	})

	It("does not report ConfigurationRolledOut=True while a volume has produced no verdict", func() {
		ctx := flow.BeginRootReconcile(context.Background()).Ctx()
		rvs := []rvView{{name: "rv-1"}}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Rollout classification: mutually exclusive categories
//

var _ = Describe("computeActualRVConfigurationCategory", func() {
	// The storage class published configuration generation 2.
	const publishedGeneration int64 = 2

	var rsc *v1alpha1.ReplicatedStorageClass

	BeforeEach(func() {
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: publishedGeneration},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: publishedGeneration,
			},
		}
	})

	// newTrackedRV builds an Auto-mode volume with the given generation state and conditions.
	newTrackedRV := func(name string, applied, observed int64, configReady, layoutConverged rvViewCondition) rvView {
		return rvView{
			name:                            name,
			configurationGeneration:         applied,
			configurationObservedGeneration: observed,
			conditions: rvViewConditions{
				satisfyEligibleNodes: condTrue(),
				configurationReady:   configReady,
				layoutConverged:      layoutConverged,
			},
		}
	}

	Describe("named cases", func() {
		It("classifies a fully aligned volume as aligned", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condTrue(), condTrue())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryAligned))
		})

		It("classifies a volume that has not observed the configuration as pending", func() {
			rv := newTrackedRV("rv-1", 1, 1, condTrue(), condTrue())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("classifies a volume with an unset observed generation as pending", func() {
			// Absence of data is not a completion signal.
			rv := newTrackedRV("rv-1", 0, 0, condMissing(), condMissing())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("classifies LayoutConverged=Unknown (volume deleting) as pending", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condTrue(), condUnknown())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("classifies a condition left over from an older volume generation as pending", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condStale(), condTrue())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("classifies a missing tracked condition as pending", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condTrue(), condMissing())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("classifies a volume with a False tracked condition as stale", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condTrue(), condFalse())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryStale))
		})

		It("classifies a held volume as stale exactly once", func() {
			// Held by NewVolumesOnly: it observed generation 2 but still runs generation 1, and
			// it reports ConfigurationReady=False/NewerConfigurationHeld. Both signals point at
			// the same category, so the classification does not double-count.
			rv := newTrackedRV("rv-1", 1, publishedGeneration, condFalse(), condTrue())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryStale))

			counters := computeActualVolumesSummary(rsc, []rvView{rv})
			Expect(*counters.StaleConfiguration).To(Equal(int32(1)))
			Expect(*counters.PendingObservation).To(Equal(int32(0)))
			Expect(*counters.Aligned).To(Equal(int32(0)))
		})

		It("gives pending precedence over stale when one condition is Unknown and the other False", func() {
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condUnknown(), condFalse())
			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryPending))
		})

		It("keeps a placement conflict off the configuration axis", func() {
			// SatisfyEligibleNodes=False is a separate concern: the volume still runs the
			// current configuration, so on the configuration axis it is aligned.
			rv := newTrackedRV("rv-1", publishedGeneration, publishedGeneration, condTrue(), condTrue())
			rv.conditions.satisfyEligibleNodes = condFalse()

			Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(rvConfigurationCategoryAligned))

			counters := computeActualVolumesSummary(rsc, []rvView{rv})
			Expect(*counters.Aligned).To(Equal(int32(1)))
			Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
			Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
		})
	})

	Describe("Manual-mode volumes", func() {
		It("excludes them from the rollout categories but still counts conflicts", func() {
			manual := rvView{
				name:              "rv-manual",
				configurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual,
				conditions: rvViewConditions{
					satisfyEligibleNodes: condFalse(),
				},
			}
			auto := newTrackedRV("rv-auto", publishedGeneration, publishedGeneration, condTrue(), condTrue())

			counters := computeActualVolumesSummary(rsc, []rvView{manual, auto})

			Expect(*counters.Total).To(Equal(int32(2)))
			Expect(*counters.Aligned).To(Equal(int32(1)))
			Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
			Expect(*counters.PendingObservation).To(Equal(int32(0)))
			Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
		})
	})

	Describe("Cartesian truth table", func() {
		type condState struct {
			name string
			cond rvViewCondition
			// noVerdict marks the states that carry no verdict about the current volume state.
			noVerdict bool
			// denies marks the states that explicitly deny the tracked property.
			denies bool
		}

		condStates := []condState{
			{name: "missing", cond: condMissing(), noVerdict: true},
			{name: "unknown", cond: condUnknown(), noVerdict: true},
			{name: "stale", cond: condStale(), noVerdict: true},
			{name: "false", cond: condFalse(), denies: true},
			{name: "true", cond: condTrue()},
		}

		type generationState struct {
			name     string
			applied  int64
			observed int64
			// notObserved marks that the volume has not seen the published generation.
			notObserved bool
			// notApplied marks that the volume runs an older configuration.
			notApplied bool
		}

		generationStates := []generationState{
			{name: "observed+applied", applied: publishedGeneration, observed: publishedGeneration},
			{name: "observed+held", applied: 1, observed: publishedGeneration, notApplied: true},
			{name: "unobserved+applied", applied: publishedGeneration, observed: 1, notObserved: true},
			{name: "unobserved+unapplied", applied: 1, observed: 1, notObserved: true, notApplied: true},
		}

		It("puts every combination in exactly one category, in pending → stale → aligned order", func() {
			var all []rvView
			var wantPending, wantStale, wantAligned int32

			for _, gen := range generationStates {
				for _, ready := range condStates {
					for _, layout := range condStates {
						rv := newTrackedRV(
							gen.name+"/"+ready.name+"/"+layout.name,
							gen.applied, gen.observed, ready.cond, layout.cond,
						)

						// Expected category, straight from the normative rules:
						//   pending — generation not observed, or a tracked condition has no verdict;
						//   stale   — otherwise, older configuration applied or a tracked condition denies;
						//   aligned — otherwise.
						var want rvConfigurationCategory
						switch {
						case gen.notObserved || ready.noVerdict || layout.noVerdict:
							want = rvConfigurationCategoryPending
							wantPending++
						case gen.notApplied || ready.denies || layout.denies:
							want = rvConfigurationCategoryStale
							wantStale++
						default:
							want = rvConfigurationCategoryAligned
							wantAligned++
						}

						Expect(computeActualRVConfigurationCategory(rsc, &rv)).To(Equal(want), "case %s", rv.name)
						all = append(all, rv)
					}
				}
			}

			// The counters must reproduce the same classification and satisfy the invariant
			// trackedTotal == pending + stale + aligned.
			counters := computeActualVolumesSummary(rsc, all)
			Expect(*counters.PendingObservation).To(Equal(wantPending))
			Expect(*counters.StaleConfiguration).To(Equal(wantStale))
			Expect(*counters.Aligned).To(Equal(wantAligned))
			Expect(*counters.PendingObservation + *counters.StaleConfiguration + *counters.Aligned).
				To(Equal(*counters.Total))
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Projection of real ReplicatedVolume objects
//

var _ = Describe("newRVView", func() {
	// newRV builds a ReplicatedVolume at generation 3 with the given tracked-condition
	// observed generations.
	newRV := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1", Generation: 3},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 2,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					ReplicatedStoragePoolName: "pool-a",
				},
			},
		}
	}

	It("projects generations, mode and the storage pool", func() {
		view := newRVView(newRV())

		Expect(view.name).To(Equal("rv-1"))
		Expect(view.configurationMode).To(Equal(v1alpha1.ReplicatedVolumeConfigurationMode("")), "empty mode means Auto")
		Expect(isRVTrackedByRSCRollout(&view)).To(BeTrue())
		Expect(view.configurationGeneration).To(Equal(int64(1)))
		Expect(view.configurationObservedGeneration).To(Equal(int64(2)))
		Expect(view.replicatedStoragePoolName).To(Equal("pool-a"))
	})

	It("excludes Manual-mode volumes from the rollout", func() {
		rv := newRV()
		rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual

		view := newRVView(rv)

		Expect(isRVTrackedByRSCRollout(&view)).To(BeFalse())
	})

	It("leaves an absent condition unset", func() {
		view := newRVView(newRV())

		Expect(view.conditions.layoutConverged).To(Equal(rvViewCondition{}))
		Expect(view.conditions.layoutConverged.hasNoVerdict()).To(BeTrue())
	})

	It("marks a condition written for the current generation as current", func() {
		rv := newRV()
		rv.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged,
			ObservedGeneration: 3,
		}}

		view := newRVView(rv)

		Expect(view.conditions.layoutConverged).To(Equal(condTrue()))
	})

	It("marks a condition left over from an older generation as not current", func() {
		rv := newRV()
		rv.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged,
			ObservedGeneration: 2,
		}}

		view := newRVView(rv)

		Expect(view.conditions.layoutConverged).To(Equal(condStale()))
		Expect(view.conditions.layoutConverged.hasNoVerdict()).To(BeTrue())
	})

	It("classifies a real held volume as stale", func() {
		// End-to-end shape of a volume held by NewVolumesOnly, as rv_controller writes it.
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 2},
			Status:     v1alpha1.ReplicatedStorageClassStatus{ConfigurationGeneration: 2},
		}
		rv := newRV()
		rv.Status.Conditions = []metav1.Condition{
			{
				Type:               v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
				Status:             metav1.ConditionFalse,
				Reason:             v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld,
				ObservedGeneration: 3,
			},
			{
				Type:               v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged,
				ObservedGeneration: 3,
			},
		}

		view := newRVView(rv)

		Expect(computeActualRVConfigurationCategory(rsc, &view)).To(Equal(rvConfigurationCategoryStale))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ConfigurationRolledOut reason depends on the rollout strategy
//

var _ = Describe("ConfigurationRolledOut rollout strategy reason", func() {
	var (
		ctx context.Context
		rsc *v1alpha1.ReplicatedStorageClass
	)

	BeforeEach(func() {
		ctx = flow.BeginRootReconcile(context.Background()).Ctx()
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 2},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.EligibleNodesConflictResolutionManual,
				},
			},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 2,
			},
		}
	})

	// heldRV is a volume held back by NewVolumesOnly: observed generation 2, still running 1.
	heldRV := func() rvView {
		return rvView{
			name:                            "rv-1",
			configurationGeneration:         1,
			configurationObservedGeneration: 2,
			conditions: rvViewConditions{
				satisfyEligibleNodes: condTrue(),
				configurationReady:   condFalse(),
				layoutConverged:      condTrue(),
			},
		}
	}

	It("reports the rollout as disabled under NewVolumesOnly", func() {
		rsc.Spec.ConfigurationRolloutStrategy = &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
			Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
		}

		Expect(ensureVolumeSummaryAndConditions(ctx, rsc, []rvView{heldRV()}).Error()).To(BeNil())

		Expect(*rsc.Status.Volumes.StaleConfiguration).To(Equal(int32(1)))
		cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))
	})

	It("reports the rollout as in progress under RollingUpdate", func() {
		rsc.Spec.ConfigurationRolloutStrategy = &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
			Type: v1alpha1.ConfigurationRolloutRollingUpdate,
			RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
				MaxParallel: 5,
			},
		}

		Expect(ensureVolumeSummaryAndConditions(ctx, rsc, []rvView{heldRV()}).Error()).To(BeNil())

		cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress))
	})

	It("treats a not-yet-defaulted strategy as RollingUpdate", func() {
		rsc.Spec.ConfigurationRolloutStrategy = nil

		Expect(ensureVolumeSummaryAndConditions(ctx, rsc, []rvView{heldRV()}).Error()).To(BeNil())

		cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress))
	})

	It("handles a storage class whose strategies are not defaulted yet", func() {
		// Both strategy fields are optional pointers filled by reconcileDefaults. Reading either
		// of them must not depend on that having happened yet.
		rsc.Spec.ConfigurationRolloutStrategy = nil
		rsc.Spec.EligibleNodesConflictResolutionStrategy = nil

		conflicting := heldRV()
		conflicting.conditions.satisfyEligibleNodes = condFalse()

		Expect(func() {
			Expect(ensureVolumeSummaryAndConditions(ctx, rsc, []rvView{conflicting}).Error()).To(BeNil())
		}).NotTo(Panic())

		rolledOut := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(rolledOut).NotTo(BeNil())
		Expect(rolledOut.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress))

		// A not-yet-defaulted conflict resolution strategy reads as RollingRepair, the default
		// the controller is about to write.
		satisfy := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(satisfy).NotTo(BeNil())
		Expect(satisfy.Status).To(Equal(metav1.ConditionFalse))
		Expect(satisfy.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonConflictResolutionInProgress))
	})
})
