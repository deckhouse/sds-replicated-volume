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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Configuration rollout strategy (NewVolumesOnly) and RSC status freshness
//

// oldConfiguration is the configuration an "existing" volume already runs.
func oldConfiguration() *v1alpha1.ReplicatedVolumeConfiguration {
	return &v1alpha1.ReplicatedVolumeConfiguration{
		Topology:                        v1alpha1.TopologyIgnored,
		FailuresToTolerate:              0,
		GuaranteedMinimumDataRedundancy: 0,
		VolumeAccess:                    v1alpha1.VolumeAccessLocal,
		ReplicatedStoragePoolName:       "test-pool",
	}
}

// newConfiguration differs from oldConfiguration in the replication parameters, i.e. it is the
// kind of change that rewrites the intended layout of every volume of the class.
func newConfiguration() *v1alpha1.ReplicatedVolumeConfiguration {
	return &v1alpha1.ReplicatedVolumeConfiguration{
		Topology:                        v1alpha1.TopologyIgnored,
		FailuresToTolerate:              1,
		GuaranteedMinimumDataRedundancy: 1,
		VolumeAccess:                    v1alpha1.VolumeAccessLocal,
		ReplicatedStoragePoolName:       "test-pool",
	}
}

// newRolloutRSC builds an RSC whose published configuration is in sync with its spec generation.
func newRolloutRSC(
	generation int64,
	config *v1alpha1.ReplicatedVolumeConfiguration,
	strategy *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy,
) *v1alpha1.ReplicatedStorageClass {
	return &v1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: generation},
		Spec: v1alpha1.ReplicatedStorageClassSpec{
			ConfigurationRolloutStrategy: strategy,
		},
		Status: v1alpha1.ReplicatedStorageClassStatus{
			ConfigurationGeneration: generation,
			Configuration:           config,
		},
	}
}

func newVolumesOnlyStrategy() *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy {
	return &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
		Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
	}
}

func rollingUpdateStrategy() *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy {
	return maxParallelStrategy(5)
}

// maxParallelStrategy is a RollingUpdate strategy with an explicit rollout budget.
func maxParallelStrategy(maxParallel int32) *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy {
	return &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
		Type: v1alpha1.ConfigurationRolloutRollingUpdate,
		RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
			MaxParallel: maxParallel,
		},
	}
}

// newRolloutRV builds an RV in normal operation (formation finished) with the given applied
// configuration state. A nil config means the volume never received a configuration.
func newRolloutRV(
	config *v1alpha1.ReplicatedVolumeConfiguration,
	appliedGeneration, observedGeneration int64,
) *v1alpha1.ReplicatedVolume {
	return newRolloutRVNamed("rv-1", config, appliedGeneration, observedGeneration)
}

// newRolloutRVNamed is newRolloutRV with an explicit name: the rollout budget is decided over a
// whole storage class, so its tests need several volumes at once.
func newRolloutRVNamed(
	name string,
	config *v1alpha1.ReplicatedVolumeConfiguration,
	appliedGeneration, observedGeneration int64,
) *v1alpha1.ReplicatedVolume {
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Generation: 1,
			Finalizers: []string{v1alpha1.RVControllerFinalizer},
			Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       resource.MustParse("10Gi"),
			ReplicatedStorageClassName: "rsc-1",
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision:                1, // Normal operation (not forming).
			Configuration:                   config,
			ConfigurationGeneration:         appliedGeneration,
			ConfigurationObservedGeneration: observedGeneration,
		},
	}
	return rv
}

var _ = Describe("reconcileRVConfiguration rollout strategy", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	reconcileOnce := func(ctx SpecContext, rv *v1alpha1.ReplicatedVolume, rsc *v1alpha1.ReplicatedStorageClass) v1alpha1.ReplicatedVolume {
		GinkgoHelper()

		rsp := newTestRSP("test-pool")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		return updated
	}

	Context("NewVolumesOnly", func() {
		It("holds a newer configuration on an existing volume instead of applying it", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), newVolumesOnlyStrategy())
			rv := newRolloutRV(oldConfiguration(), 1, 1)

			updated := reconcileOnce(ctx, rv, rsc)

			// Content and the applied generation must stay untouched.
			Expect(updated.Status.Configuration).To(Equal(oldConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
			// The volume observed the new configuration (so the class aggregate does not hang
			// in pendingObservation), it just does not apply it.
			Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(2)))

			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld))
			Expect(cond.Message).To(ContainSubstring("2"), "message must name the held (newer) generation")
			Expect(cond.Message).To(ContainSubstring("1"), "message must name the applied generation")
		})

		It("applies the configuration to a volume that never received one", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), newVolumesOnlyStrategy())
			rv := newRolloutRV(nil, 0, 0)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))
			Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(2)))

			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))
		})

		It("keeps an already-applied configuration ready (nothing to hold)", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), newVolumesOnlyStrategy())
			rv := newRolloutRV(newConfiguration(), 2, 2)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))

			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))
		})

		It("does not hold a volume after the strategy switch itself (RollingUpdate → NewVolumesOnly)", func(ctx SpecContext) {
			// Switching the strategy is a spec edit: it bumps the class metadata.generation, and
			// the class controller republishes the very same configuration content under the new
			// generation 3. The volume already runs that content under generation 2, so there is
			// nothing to hold back — it must simply adopt the new generation and stay Ready.
			//
			// This is the regression guard for the branch order inside reconcileRVConfiguration:
			// the content fast-path must run BEFORE the NewVolumesOnly hold. The hold branch does
			// not compare content, so with the branches swapped this volume would be pinned at
			// generation 2 with False/NewerConfigurationHeld forever, and the class would report
			// it stale even though it runs exactly the published configuration.
			rsc := newRolloutRSC(3, newConfiguration(), newVolumesOnlyStrategy())
			rv := newRolloutRV(newConfiguration(), 2, 2)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(3)))
			Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(3)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsTrue().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady).Eval()).To(BeTrue())
		})
	})

	Context("RollingUpdate", func() {
		It("applies a newer configuration to an existing volume", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), rollingUpdateStrategy())
			rv := newRolloutRV(oldConfiguration(), 1, 1)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))
			Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(2)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsTrue().Eval()).To(BeTrue())
		})

		It("applies a newer configuration when the strategy is not defaulted yet (nil = RollingUpdate)", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), nil)
			rv := newRolloutRV(oldConfiguration(), 1, 1)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))
		})

		It("rolls out a held volume after the strategy switch (NewVolumesOnly → RollingUpdate)", func(ctx SpecContext) {
			// The volume was held: it observed generation 2 but still runs generation 1.
			rsc := newRolloutRSC(2, newConfiguration(), rollingUpdateStrategy())
			rv := newRolloutRV(oldConfiguration(), 1, 2)
			obju.SetStatusCondition(rv, metav1.Condition{
				Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
				Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld,
			})

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsTrue().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady).Eval()).To(BeTrue())
		})
	})

	Context("held configuration that cannot be replaced", func() {
		// The newer configuration is TransZonal with FTT=1/GMDR=1, which needs 3 zones; the RSP
		// has none. Under NewVolumesOnly the volume must stay on its own configuration and report
		// the honest "held" reason — the controller must not silently "fix" anything.
		invalidNewConfig := func() *v1alpha1.ReplicatedVolumeConfiguration {
			return &v1alpha1.ReplicatedVolumeConfiguration{
				Topology:                        v1alpha1.TopologyTransZonal,
				FailuresToTolerate:              1,
				GuaranteedMinimumDataRedundancy: 1,
				VolumeAccess:                    v1alpha1.VolumeAccessLocal,
				ReplicatedStoragePoolName:       "test-pool",
			}
		}

		It("keeps holding when the newer configuration is invalid", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, invalidNewConfig(), newVolumesOnlyStrategy())
			rv := newRolloutRV(oldConfiguration(), 1, 1)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(oldConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsFalse().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld).Eval()).To(BeTrue())
		})

		It("reports the invalid configuration honestly once the strategy allows the rollout", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, invalidNewConfig(), rollingUpdateStrategy())
			rv := newRolloutRV(oldConfiguration(), 1, 2)

			updated := reconcileOnce(ctx, rv, rsc)

			// Nothing is applied, but the reason now names the real blocker.
			Expect(updated.Status.Configuration).To(Equal(oldConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsFalse().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration).Eval()).To(BeTrue())
		})
	})

	Context("RSC status freshness", func() {
		It("does not apply a configuration the storage class has not republished yet (new volume)", func(ctx SpecContext) {
			// spec generation 2, status still carries generation 1: the class controller has not
			// accepted the edit yet. Applying generation 1 here would freeze it forever under
			// NewVolumesOnly, because the volume stops being "new" the moment it gets a config.
			rsc := newRolloutRSC(1, oldConfiguration(), newVolumesOnlyStrategy())
			rsc.Generation = 2
			rv := newRolloutRV(nil, 0, 0)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(BeNil())
			Expect(updated.Status.ConfigurationGeneration).To(BeZero())
			Expect(updated.Status.ConfigurationObservedGeneration).To(BeZero())
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsFalse().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass).Eval()).To(BeTrue())
		})

		It("does not apply a stale published configuration to an existing volume either", func(ctx SpecContext) {
			rsc := newRolloutRSC(2, newConfiguration(), rollingUpdateStrategy())
			rsc.Generation = 3
			rv := newRolloutRV(oldConfiguration(), 1, 1)

			updated := reconcileOnce(ctx, rv, rsc)

			Expect(updated.Status.Configuration).To(Equal(oldConfiguration()))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
			Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(1)))
			Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsFalse().
				ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass).Eval()).To(BeTrue())
		})

		It("applies the configuration once the storage class publishes the current generation", func(ctx SpecContext) {
			rsc := newRolloutRSC(1, oldConfiguration(), newVolumesOnlyStrategy())
			rsc.Generation = 2
			rv := newRolloutRV(nil, 0, 0)
			rsp := newTestRSP("test-pool")

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			// Pass 1: the class status is stale, nothing is applied.
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var afterWait v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &afterWait)).To(Succeed())
			Expect(afterWait.Status.Configuration).To(BeNil())

			// The class controller publishes generation 2.
			var storedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rsc), &storedRSC)).To(Succeed())
			storedRSC.Status.Configuration = newConfiguration()
			storedRSC.Status.ConfigurationGeneration = 2
			Expect(cl.Status().Update(ctx, &storedRSC)).To(Succeed())

			// Pass 2: the volume applies the current configuration.
			_, err = rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var afterPublish v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &afterPublish)).To(Succeed())
			Expect(afterPublish.Status.Configuration).To(Equal(newConfiguration()))
			Expect(afterPublish.Status.ConfigurationGeneration).To(Equal(int64(2)))
			Expect(obju.StatusCondition(&afterPublish, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
				IsTrue().Eval()).To(BeTrue())
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation restart must not erase the applied configuration
//

var _ = Describe("Formation restart configuration preservation", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// newRestartingRV builds an RV whose formation started long enough ago for the restart
	// timeout to have passed, with an unscheduled replica that keeps formation waiting.
	newRestartingRV := func() (*v1alpha1.ReplicatedVolume, *v1alpha1.ReplicatedVolumeReplica) {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Generation: 1,
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration:                   oldConfiguration(),
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 1,
				DatameshRevision:                1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:       "old-secret",
					SharedSecretAlg:    v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"},
					Size:               resource.MustParse("10Gi"),
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkFormationTransitionWithTime(formationStepIdxPreconfigure, metav1.NewTime(time.Now().Add(-1*time.Hour))),
				},
			},
		}
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
		return rv, rvr
	}

	It("keeps the applied configuration when the storage class is temporarily unavailable", func(ctx SpecContext) {
		rv, rvr := newRestartingRV()
		rsp := newTestRSP("test-pool")

		// No RSC object: the restart cannot re-derive anything.
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsp, rvr).
			WithStatusSubresource(rv, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		// Datamesh state is reset, but the configuration survives the restart.
		Expect(updated.Status.DatameshRevision).To(BeZero())
		Expect(updated.Status.Datamesh.SharedSecret).To(BeEmpty())
		Expect(updated.Status.Configuration).To(Equal(oldConfiguration()))
		Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
		Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(1)))
	})

	It("does not turn a restarting volume into a new one under NewVolumesOnly", func(ctx SpecContext) {
		rv, rvr := newRestartingRV()
		rsc := newRolloutRSC(2, newConfiguration(), newVolumesOnlyStrategy())
		rsp := newTestRSP("test-pool")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		Expect(updated.Status.Configuration).To(Equal(oldConfiguration()),
			"a restarting volume must not pick up a configuration held from it")
		Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(1)))
		Expect(updated.Status.ConfigurationObservedGeneration).To(Equal(int64(2)))
		Expect(obju.StatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld).Eval()).To(BeTrue())
	})

	It("re-derives the current configuration on restart under RollingUpdate", func(ctx SpecContext) {
		rv, rvr := newRestartingRV()
		rsc := newRolloutRSC(2, newConfiguration(), rollingUpdateStrategy())
		rsp := newTestRSP("test-pool")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		Expect(updated.Status.Configuration).To(Equal(newConfiguration()))
		Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(2)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Configuration rollout budget (RollingUpdate.maxParallel)
//
// oldConfiguration is a 1D layout (FTT=0, GMDR=0), newConfiguration a 3D one (FTT=1, GMDR=1):
// adopting the latter is the class-wide migration the budget throttles.
//

// r2Configuration asks for the 2D+1TB layout (FTT=1, GMDR=0) — the shape an r3→r2 edit produces,
// and the one an r2→r3 edit can never leave (convergence never creates a Diskful replica).
func r2Configuration() *v1alpha1.ReplicatedVolumeConfiguration {
	return &v1alpha1.ReplicatedVolumeConfiguration{
		Topology:                        v1alpha1.TopologyIgnored,
		FailuresToTolerate:              1,
		GuaranteedMinimumDataRedundancy: 0,
		VolumeAccess:                    v1alpha1.VolumeAccessLocal,
		ReplicatedStoragePoolName:       "test-pool",
	}
}

// intendedRolloutGeneration is the generation the class published its current configuration
// under. Volumes that adopted that configuration carry it in status.configurationGeneration;
// anything lower belongs to an earlier configuration epoch.
const intendedRolloutGeneration = 2

// asAdopted moves rv onto the intended configuration the way the gate does: the content and the
// generation it came from are written together.
func asAdopted(rv *v1alpha1.ReplicatedVolume) *v1alpha1.ReplicatedVolume {
	rv.Status.Configuration = newConfiguration()
	rv.Status.ConfigurationGeneration = intendedRolloutGeneration
	return rv
}

// withConvergenceVerdict publishes the MembershipLayoutConverged verdict reconcileLayoutStatus
// would write for the current spec generation of rv. It is that verdict — not the member counts —
// that decides whether a volume carrying the intended configuration still holds a rollout slot.
func withConvergenceVerdict(
	rv *v1alpha1.ReplicatedVolume,
	status metav1.ConditionStatus,
	reason string,
) *v1alpha1.ReplicatedVolume {
	obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
		Status:  status,
		Reason:  reason,
		Message: "set by the test fixture",
	})
	return rv
}

// asConverged is the verdict of a volume that has finished rolling out.
func asConverged(rv *v1alpha1.ReplicatedVolume) *v1alpha1.ReplicatedVolume {
	return withConvergenceVerdict(rv, metav1.ConditionTrue,
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged)
}

// withLayout gives rv a datamesh of the requested composition. Matching member counts alone never
// release a rollout slot (see withConvergenceVerdict); the composition is what the real
// convergence code reads once such a volume is reconciled.
func withLayout(rv *v1alpha1.ReplicatedVolume, diskful, tiebreakers int) *v1alpha1.ReplicatedVolume {
	members := make([]v1alpha1.DatameshMember, 0, diskful+tiebreakers)
	for i := range diskful + tiebreakers {
		memberType := v1alpha1.DatameshMemberTypeDiskful
		if i >= diskful {
			memberType = v1alpha1.DatameshMemberTypeTieBreaker
		}
		members = append(members, v1alpha1.DatameshMember{
			Name: v1alpha1.FormatReplicatedVolumeReplicaName(rv.Name, uint8(i)),
			Type: memberType,
		})
	}
	rv.Status.Datamesh.Members = members
	return rv
}

var _ = Describe("configuration rollout budget", func() {
	Describe("computeIntendedRolloutMaxParallel", func() {
		DescribeTable("resolves the budget of a storage class",
			func(strategy *v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy, want int) {
				rsc := newRolloutRSC(1, newConfiguration(), strategy)
				Expect(computeIntendedRolloutMaxParallel(rsc)).To(Equal(want))
			},
			Entry("explicit budget", maxParallelStrategy(2), 2),
			Entry("strategy not defaulted yet", nil, defaultConfigurationRolloutMaxParallel),
			Entry("RollingUpdate without parameters",
				&v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ConfigurationRolloutRollingUpdate,
				},
				defaultConfigurationRolloutMaxParallel),
			// The schema enforces a minimum of 1, so a zero here means the object bypassed it;
			// honouring it literally would stall the rollout of the whole class forever.
			Entry("budget below the schema minimum", maxParallelStrategy(0), defaultConfigurationRolloutMaxParallel),
		)
	})

	Describe("computeActualRolloutRole", func() {
		DescribeTable("classifies a volume against the configuration its class intends",
			func(mutate func(*v1alpha1.ReplicatedVolume), want rolloutRole) {
				rv := newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1)
				mutate(rv)
				Expect(computeActualRolloutRole(rv, "rsc-1", newConfiguration(), intendedRolloutGeneration)).
					To(Equal(want))
			},
			Entry("stores an older configuration",
				func(*v1alpha1.ReplicatedVolume) {}, rolloutRolePending),
			Entry("stores the intended configuration but has not reached its layout",
				func(rv *v1alpha1.ReplicatedVolume) {
					withLayout(asAdopted(rv), 1, 0)
				}, rolloutRoleActive),
			Entry("stores the intended configuration and reported it converged",
				func(rv *v1alpha1.ReplicatedVolume) {
					asConverged(withLayout(asAdopted(rv), 3, 0))
				}, rolloutRoleConverged),
			// A → B → A: the content of a volume that converged on the first A is identical to
			// the content of one that just adopted the second, and its Converged verdict is real
			// — it simply belongs to the earlier epoch. Only the generation tells them apart.
			Entry("converged on identical content published under an earlier generation",
				func(rv *v1alpha1.ReplicatedVolume) {
					asConverged(withLayout(asAdopted(rv), 3, 0))
					rv.Status.ConfigurationGeneration = intendedRolloutGeneration - 1
				}, rolloutRoleActive),
			// The member is retyped before its transition completes, so the counted layout can
			// match the intended one while data is still moving.
			Entry("reached the intended member counts but is still moving members",
				func(rv *v1alpha1.ReplicatedVolume) {
					withLayout(asAdopted(rv), 3, 0)
					rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
						Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
						FromReplicaType: v1alpha1.ReplicaTypeDiskful,
						ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
					}}
					withConvergenceVerdict(rv, metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging)
				}, rolloutRoleActive),
			// A configuration reverted mid-flight leaves a replica whose spec.type is already
			// flipped while the member it belongs to is not: the counts match the intended layout,
			// the volume does not. The signal lives in the replicas, which this classifier does
			// not read — only the verdict computed from them keeps the slot occupied.
			Entry("reached the intended member counts while a retype is still pending",
				func(rv *v1alpha1.ReplicatedVolume) {
					withLayout(asAdopted(rv), 3, 0)
					withConvergenceVerdict(rv, metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging)
				}, rolloutRoleActive),
			// Same shape, permanent: a tie-breaker is terminating and its replacement cannot be
			// placed on any eligible node. The counts still match, and the volume must keep its
			// slot for as long as it is stuck — that is the blast radius the budget buys.
			Entry("reached the intended member counts but cannot place a replacement",
				func(rv *v1alpha1.ReplicatedVolume) {
					withLayout(asAdopted(rv), 3, 0)
					withConvergenceVerdict(rv, metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge)
				}, rolloutRoleActive),
			// Reachable for one pass right after formation, which does not report layout status.
			Entry("has not reported on its layout yet",
				func(rv *v1alpha1.ReplicatedVolume) {
					withLayout(asAdopted(rv), 3, 0)
				}, rolloutRoleActive),
			// A verdict from before the last spec edit says nothing about the current one.
			Entry("reported convergence for an older spec generation",
				func(rv *v1alpha1.ReplicatedVolume) {
					asConverged(withLayout(asAdopted(rv), 3, 0))
					rv.Generation++
				}, rolloutRoleActive),
			Entry("is being deleted",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.DeletionTimestamp = ptr.To(metav1.Now())
				}, rolloutRoleExcluded),
			Entry("takes its configuration from the volume spec",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
				}, rolloutRoleExcluded),
			Entry("belongs to another storage class",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Spec.ReplicatedStorageClassName = "rsc-2"
				}, rolloutRoleExcluded),
			Entry("never received a configuration",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.Configuration = nil
				}, rolloutRoleExcluded),
			Entry("is still forming",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.DatameshRevision = 0
				}, rolloutRoleExcluded),
		)
	})

	Describe("computeActualRolloutCohort", func() {
		It("counts the volumes rolling out and queues the rest by name", func() {
			converged := asConverged(withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 3, 0))
			active := withLayout(newRolloutRVNamed("rv-1", newConfiguration(), 2, 2), 1, 0)
			pendingLate := newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1)
			pendingEarly := newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1)

			cohort := computeActualRolloutCohort(
				pendingLate,
				[]*v1alpha1.ReplicatedVolume{pendingLate, converged, pendingEarly, active},
				newConfiguration(), intendedRolloutGeneration)

			Expect(cohort.activeCount).To(Equal(1))
			Expect(cohort.pendingNames).To(Equal([]string{"rv-2", "rv-3"}))
		})

		It("prefers the volume being reconciled over its listed copy", func() {
			// The listing predates the write that gave rv-0 the intended configuration.
			listed := newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1)
			self := withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0)

			cohort := computeActualRolloutCohort(self, []*v1alpha1.ReplicatedVolume{listed},
				newConfiguration(), intendedRolloutGeneration)

			Expect(cohort.activeCount).To(Equal(1))
			Expect(cohort.pendingNames).To(BeEmpty())
		})

		It("frees the slot on the verdict the controller itself publishes", func(ctx SpecContext) {
			// The other half of the rule that an unreported volume keeps its slot: holding it
			// costs exactly one pass. Nothing is written by hand here — the volume has reached
			// the layout its configuration asks for, and reconcileLayoutStatus (the single writer
			// of the condition) is run over it exactly as the normal-operation pass does.
			rv, rvrs := convergenceFixture(1, 3, 0) // an r3 configuration at a 3D layout
			rv.Status.DatameshRevision = 1          // formation finished
			intended := rv.Status.Configuration.DeepCopy()
			generation := rv.Status.ConfigurationGeneration

			Expect(computeActualRolloutRole(rv, "", intended, generation)).To(Equal(rolloutRoleActive),
				"a volume that has not reported on its layout keeps its slot")

			Expect(NewReconciler(nil, nil).reconcileLayoutStatus(ctx, rv, rvrs, nil).Error()).
				NotTo(HaveOccurred())

			Expect(computeActualRolloutRole(rv, "", intended, generation)).To(Equal(rolloutRoleConverged))
		})

		It("accounts for the volume being reconciled even when the listing misses it", func() {
			self := newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1)

			cohort := computeActualRolloutCohort(self, nil, newConfiguration(), intendedRolloutGeneration)

			Expect(cohort.pendingNames).To(Equal([]string{"rv-0"}))
		})
	})

	Describe("computeTargetRolloutAdmission", func() {
		// pendingClass is the whole class waiting for one configuration change: four volumes,
		// none of them started yet.
		pendingClass := func() []*v1alpha1.ReplicatedVolume {
			return []*v1alpha1.ReplicatedVolume{
				newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1),
				newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
				newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
				newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1),
			}
		}

		// admittedFor is what the workers of the class conclude, each volume deciding for itself
		// from the snapshot its own worker happens to read.
		admittedFor := func(
			intended *v1alpha1.ReplicatedVolumeConfiguration,
			intendedGeneration int64,
			snapshot []*v1alpha1.ReplicatedVolume,
			deciding []*v1alpha1.ReplicatedVolume,
			maxParallel int,
		) []string {
			var admitted []string
			for _, rv := range deciding {
				cohort := computeActualRolloutCohort(rv, snapshot, intended, intendedGeneration)
				if computeTargetRolloutAdmission(rv, cohort, maxParallel) {
					admitted = append(admitted, rv.Name)
				}
			}
			return admitted
		}

		// admittedFrom is admittedFor against the configuration the class currently publishes,
		// with every worker reading the same listing.
		admittedFrom := func(snapshot []*v1alpha1.ReplicatedVolume, deciding []*v1alpha1.ReplicatedVolume, maxParallel int) []string {
			return admittedFor(newConfiguration(), intendedRolloutGeneration, snapshot, deciding, maxParallel)
		}

		It("admits the first maxParallel volumes by name and no others", func() {
			class := pendingClass()
			Expect(admittedFrom(class, class, 2)).To(Equal([]string{"rv-0", "rv-1"}))
		})

		It("reaches the same verdict whatever order the listing arrives in", func() {
			class := pendingClass()
			reversed := []*v1alpha1.ReplicatedVolume{class[3], class[2], class[1], class[0]}

			Expect(admittedFrom(reversed, reversed, 2)).To(ConsistOf("rv-0", "rv-1"))
		})

		It("does not over-admit when every worker reads the same stale snapshot", func() {
			// rv-0 has already stored the intended configuration, but the snapshot every other
			// worker reads still shows it pending. It keeps its place at the head of the queue,
			// so the workers behind it admit one volume, not two.
			class := pendingClass()
			started := withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0)

			Expect(admittedFrom(class, []*v1alpha1.ReplicatedVolume{started, class[1], class[2], class[3]}, 2)).
				To(Equal([]string{"rv-0", "rv-1"}))
		})

		It("does not over-admit when a reverted configuration revives an old verdict", func() {
			// A → B → A, with the budget lowered from 3 to 2 by the same edit. Three volumes had
			// taken B; the class is now back on A under generation 3, and the workers read
			// different snapshots of that moment.
			//
			// The trap is the first-epoch copy of rv-0: it carries A and reports Converged, which
			// is indistinguishable from a volume that has already finished the rollout going on
			// now. Only the generation its content came from tells the two epochs apart — without
			// it rv-0 drops out of the accounting, both of its peers are admitted behind it, and
			// rv-0 itself is admitted a moment later, putting three volumes on A at maxParallel 2.
			current := oldConfiguration() // A, republished under generation 3
			const currentGeneration = 3

			atB := func(name string) *v1alpha1.ReplicatedVolume {
				return withLayout(newRolloutRVNamed(name, newConfiguration(), 2, 2), 1, 0)
			}
			rv0, rv1, rv2 := atB("rv-0"), atB("rv-1"), atB("rv-2")
			staleRV0 := asConverged(withLayout(newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1), 1, 0))

			// The workers of rv-1 and rv-2 still see rv-0 as it was one configuration ago.
			firstRound := admittedFor(current, currentGeneration,
				[]*v1alpha1.ReplicatedVolume{staleRV0, rv1, rv2},
				[]*v1alpha1.ReplicatedVolume{rv1, rv2}, 2)

			// The cache then catches up on rv-0, but not yet on what rv-1 and rv-2 just wrote.
			secondRound := admittedFor(current, currentGeneration,
				[]*v1alpha1.ReplicatedVolume{rv0, rv1, rv2},
				[]*v1alpha1.ReplicatedVolume{rv0}, 2)

			Expect(append(firstRound, secondRound...)).To(ConsistOf("rv-0", "rv-1"),
				"no more than maxParallel volumes may take the reverted configuration")
		})

		It("counts volumes that carry the configuration but have not converged", func() {
			// The two volumes that already store the intended configuration take the content-equal
			// fast path and never reach the gate again — but they are mid-migration (their layout
			// is still 1D against an intended 3D), so they hold both slots.
			rolling := []*v1alpha1.ReplicatedVolume{
				withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
				withLayout(newRolloutRVNamed("rv-1", newConfiguration(), 2, 2), 1, 0),
			}
			waiting := []*v1alpha1.ReplicatedVolume{
				newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
				newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1),
			}
			snapshot := append(append([]*v1alpha1.ReplicatedVolume{}, rolling...), waiting...)

			Expect(admittedFrom(snapshot, waiting, 2)).To(BeEmpty())

			// One of them reaches the intended layout and releases its slot; exactly the first
			// waiting volume by name takes it.
			asConverged(withLayout(rolling[0], 3, 0))
			Expect(admittedFrom(snapshot, waiting, 2)).To(Equal([]string{"rv-2"}))
		})

		It("keeps the slot of a volume whose layout matches but has not converged", func() {
			// Both states are reachable with the intended member counts already in place: a
			// pending retype (Converging) and a replacement tie-breaker that cannot be scheduled
			// (CannotConverge). Counting either as finished would hand its slot to a pending
			// volume and take the class over the budget.
			for _, reason := range []string{
				v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
				v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
			} {
				stuck := withConvergenceVerdict(
					withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 3, 0),
					metav1.ConditionFalse, reason)
				waiting := []*v1alpha1.ReplicatedVolume{newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1)}
				snapshot := append([]*v1alpha1.ReplicatedVolume{stuck}, waiting...)

				Expect(admittedFrom(snapshot, waiting, 1)).To(BeEmpty(), "reason %s", reason)
			}
		})

		It("admits nothing while more volumes are rolling out than a lowered budget allows", func() {
			rolling := []*v1alpha1.ReplicatedVolume{
				withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
				withLayout(newRolloutRVNamed("rv-1", newConfiguration(), 2, 2), 1, 0),
				withLayout(newRolloutRVNamed("rv-2", newConfiguration(), 2, 2), 1, 0),
			}
			waiting := []*v1alpha1.ReplicatedVolume{newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1)}
			snapshot := append(append([]*v1alpha1.ReplicatedVolume{}, rolling...), waiting...)

			Expect(admittedFrom(snapshot, waiting, 2)).To(BeEmpty())

			// Raising the budget widens the frontier again.
			Expect(admittedFrom(snapshot, waiting, 5)).To(Equal([]string{"rv-3"}))
		})

		It("does not throttle a volume that is outside the rollout", func() {
			class := pendingClass()
			forming := newRolloutRVNamed("rv-9", oldConfiguration(), 1, 1)
			forming.Status.DatameshRevision = 0

			Expect(admittedFrom(class, []*v1alpha1.ReplicatedVolume{forming}, 1)).To(Equal([]string{"rv-9"}))
		})
	})
})

var _ = Describe("reconcileRVConfiguration rollout budget", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	buildClass := func(rsc *v1alpha1.ReplicatedStorageClass, rvs ...*v1alpha1.ReplicatedVolume) (client.Client, *Reconciler) {
		GinkgoHelper()

		objs := []client.Object{rsc, newTestRSP("test-pool")}
		for _, rv := range rvs {
			objs = append(objs, rv)
		}
		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}, &v1alpha1.ReplicatedStorageClass{}).
			Build()
		return cl, NewReconciler(cl, scheme)
	}

	reconcileNamed := func(ctx SpecContext, rec *Reconciler, names ...string) reconcile.Result {
		GinkgoHelper()

		var last reconcile.Result
		for _, name := range names {
			result, err := rec.Reconcile(ctx, RequestFor(&v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			}))
			Expect(err).NotTo(HaveOccurred())
			last = result
		}
		return last
	}

	storedRV := func(ctx SpecContext, cl client.Client, name string) *v1alpha1.ReplicatedVolume {
		GinkgoHelper()

		var rv v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKey{Name: name}, &rv)).To(Succeed())
		return &rv
	}

	// expectRolledOut asserts the volume adopted the configuration of generation 2.
	expectRolledOut := func(rv *v1alpha1.ReplicatedVolume, config *v1alpha1.ReplicatedVolumeConfiguration) {
		GinkgoHelper()

		Expect(rv.Status.Configuration).To(Equal(config), "%s must carry the new configuration", rv.Name)
		Expect(rv.Status.ConfigurationGeneration).To(Equal(int64(2)), "%s", rv.Name)
		Expect(rv.Status.ConfigurationObservedGeneration).To(Equal(int64(2)), "%s", rv.Name)
	}

	// expectQueued asserts the volume kept the configuration of generation 1 and reports that it
	// is waiting for a rollout slot.
	expectQueued := func(rv *v1alpha1.ReplicatedVolume, config *v1alpha1.ReplicatedVolumeConfiguration) {
		GinkgoHelper()

		Expect(rv.Status.Configuration).To(Equal(config), "%s must keep its own configuration", rv.Name)
		Expect(rv.Status.ConfigurationGeneration).To(Equal(int64(1)), "%s", rv.Name)
		// The newer generation is observed even though it is not applied, so the class aggregate
		// does not hang in "pending observation" while the volume queues.
		Expect(rv.Status.ConfigurationObservedGeneration).To(Equal(int64(2)), "%s", rv.Name)
		Expect(obju.StatusCondition(rv, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress).Eval()).
			To(BeTrue(), "%s must report that it is waiting for a rollout slot", rv.Name)
	}

	It("hands the new configuration to maxParallel volumes of the class at a time", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(2))
		cl, rec := buildClass(rsc,
			newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1),
		)

		// Reconciled back to front: the order volumes are picked up in must not change who wins.
		reconcileNamed(ctx, rec, "rv-3", "rv-2", "rv-1", "rv-0")

		expectRolledOut(storedRV(ctx, cl, "rv-0"), newConfiguration())
		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
		expectQueued(storedRV(ctx, cl, "rv-2"), oldConfiguration())
		expectQueued(storedRV(ctx, cl, "rv-3"), oldConfiguration())
	})

	It("re-checks the budget on its own, without a sibling watch", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		_, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
		)

		result := reconcileNamed(ctx, rec, "rv-1")

		Expect(result.RequeueAfter).To(Equal(configurationRolloutRequeueInterval))
	})

	It("passes the slot on when a volume finishes rolling out", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(2))
		// rv-0 finished (3D, reported converged), rv-1 is still migrating.
		cl, rec := buildClass(rsc,
			asConverged(withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 3, 0)),
			withLayout(newRolloutRVNamed("rv-1", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-3", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-2", "rv-3")

		expectRolledOut(storedRV(ctx, cl, "rv-2"), newConfiguration())
		expectQueued(storedRV(ctx, cl, "rv-3"), oldConfiguration())
	})

	DescribeTable("keeps the slot of a volume whose members match but which is not converged",
		// Both verdicts are reachable with the intended member counts already in place — a retype
		// flipped on a replica spec, and a replacement tie-breaker that cannot be scheduled (see
		// the computeTargetLayoutAction specs). Counting either as finished would hand rv-1 the
		// only slot of the class while rv-0 is still mid-rollout.
		func(ctx SpecContext, reason string) {
			rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
			cl, rec := buildClass(rsc,
				withConvergenceVerdict(
					withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 3, 0),
					metav1.ConditionFalse, reason),
				newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
			)

			reconcileNamed(ctx, rec, "rv-1")

			expectQueued(storedRV(ctx, cl, "rv-1"), oldConfiguration())
		},
		Entry("a retype is still pending",
			v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging),
		Entry("a replacement tie-breaker cannot be placed",
			v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge),
	)

	It("keeps volumes that are already rolling out when the budget is lowered", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			withLayout(newRolloutRVNamed("rv-1", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-0", "rv-1", "rv-2")

		// Two volumes are over the lowered budget, but stopping them would strand half-migrated
		// layouts — they keep the configuration they started with.
		expectRolledOut(storedRV(ctx, cl, "rv-0"), newConfiguration())
		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
		expectQueued(storedRV(ctx, cl, "rv-2"), oldConfiguration())
	})

	It("widens the frontier when the budget is raised", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			newRolloutRVNamed("rv-0", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-0", "rv-1", "rv-2")
		expectQueued(storedRV(ctx, cl, "rv-2"), oldConfiguration())

		var storedRSC v1alpha1.ReplicatedStorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rsc-1"}, &storedRSC)).To(Succeed())
		storedRSC.Spec.ConfigurationRolloutStrategy = maxParallelStrategy(3)
		Expect(cl.Update(ctx, &storedRSC)).To(Succeed())

		reconcileNamed(ctx, rec, "rv-1", "rv-2")

		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
		expectRolledOut(storedRV(ctx, cl, "rv-2"), newConfiguration())
	})

	It("rolls out to five volumes at a time when the class has no explicit budget", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), nil)
		rvs := make([]*v1alpha1.ReplicatedVolume, 0, 6)
		names := make([]string, 0, 6)
		for i := range 6 {
			name := fmt.Sprintf("rv-%d", i)
			rvs = append(rvs, newRolloutRVNamed(name, oldConfiguration(), 1, 1))
			names = append(names, name)
		}
		cl, rec := buildClass(rsc, rvs...)

		reconcileNamed(ctx, rec, names...)

		for _, name := range names[:defaultConfigurationRolloutMaxParallel] {
			expectRolledOut(storedRV(ctx, cl, name), newConfiguration())
		}
		expectQueued(storedRV(ctx, cl, "rv-5"), oldConfiguration())
	})

	It("does not let volumes on their way out hold a slot", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(2))
		leaving := withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0)
		leaving.DeletionTimestamp = ptr.To(metav1.Now())
		cl, rec := buildClass(rsc,
			leaving,
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
			newRolloutRVNamed("rv-2", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-1", "rv-2")

		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
		expectRolledOut(storedRV(ctx, cl, "rv-2"), newConfiguration())
	})

	It("does not throttle a volume that never received a configuration", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-1", nil, 0, 0),
		)

		reconcileNamed(ctx, rec, "rv-1")

		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
	})

	It("does not throttle a volume configured from its own spec", func(ctx SpecContext) {
		manual := newRolloutRVNamed("rv-1", oldConfiguration(), 0, 0)
		manual.Spec.ReplicatedStorageClassName = ""
		manual.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
		manual.Spec.ManualConfiguration = newConfiguration()
		manual.Labels = nil

		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			manual,
		)

		reconcileNamed(ctx, rec, "rv-1")

		stored := storedRV(ctx, cl, "rv-1")
		Expect(stored.Status.Configuration).To(Equal(newConfiguration()))
		// Manual mode tracks no class generation at all.
		Expect(stored.Status.ConfigurationGeneration).To(BeZero())
	})

	It("adopts a new generation of the same content without taking a slot", func(ctx SpecContext) {
		// The class republished identical content under a new generation (a spec edit that does
		// not touch the configuration). The volume is already aligned, so the content fast-path
		// must run before the gate — otherwise an exhausted budget would pin it at generation 1.
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-1", newConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-1")

		stored := storedRV(ctx, cl, "rv-1")
		expectRolledOut(stored, newConfiguration())
		Expect(obju.StatusCondition(stored, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
			IsTrue().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady).Eval()).To(BeTrue())
	})

	It("holds a volume under NewVolumesOnly rather than queueing it", func(ctx SpecContext) {
		rsc := newRolloutRSC(2, newConfiguration(), newVolumesOnlyStrategy())
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", newConfiguration(), 2, 2), 1, 0),
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-1")

		stored := storedRV(ctx, cl, "rv-1")
		Expect(stored.Status.Configuration).To(Equal(oldConfiguration()))
		Expect(obju.StatusCondition(stored, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld).Eval()).To(BeTrue())
	})

	It("reports an invalid configuration rather than a queue position", func(ctx SpecContext) {
		// TransZonal with FTT=1/GMDR=1 needs three zones and the pool has none. The volume must
		// learn what actually blocks it, not that it is waiting for a slot it could not use.
		invalid := newConfiguration()
		invalid.Topology = v1alpha1.TopologyTransZonal

		rsc := newRolloutRSC(2, invalid, maxParallelStrategy(1))
		cl, rec := buildClass(rsc,
			withLayout(newRolloutRVNamed("rv-0", invalid, 2, 2), 1, 0),
			newRolloutRVNamed("rv-1", oldConfiguration(), 1, 1),
		)

		reconcileNamed(ctx, rec, "rv-1")

		stored := storedRV(ctx, cl, "rv-1")
		Expect(stored.Status.Configuration).To(Equal(oldConfiguration()))
		Expect(obju.StatusCondition(stored, v1alpha1.ReplicatedVolumeCondConfigurationReadyType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration).Eval()).To(BeTrue())
	})

	It("limits the blast radius of a configuration no volume can converge on", func(ctx SpecContext) {
		// The class is edited the wrong way round: every volume runs 2D+1TB and the new
		// configuration asks for 3D, which convergence can never reach (it never creates a
		// Diskful replica). The volumes that adopt it are stuck with it forever, so the budget is
		// the only thing that keeps the damage down to two volumes out of four.
		rsc := newRolloutRSC(2, newConfiguration(), maxParallelStrategy(2))
		rvs := make([]*v1alpha1.ReplicatedVolume, 0, 4)
		names := make([]string, 0, 4)
		for i := range 4 {
			name := fmt.Sprintf("rv-%d", i)
			rvs = append(rvs, withLayout(newRolloutRVNamed(name, r2Configuration(), 1, 1), 2, 1))
			names = append(names, name)
		}
		cl, rec := buildClass(rsc, rvs...)

		// Several passes: the stuck volumes never release their slots, so the outcome must not
		// drift with time.
		reconcileNamed(ctx, rec, names...)
		reconcileNamed(ctx, rec, names...)
		reconcileNamed(ctx, rec, names...)

		stuck := storedRV(ctx, cl, "rv-0")
		expectRolledOut(stuck, newConfiguration())
		Expect(obju.StatusCondition(stuck, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported).Eval()).
			To(BeTrue(), "rv-0 must be stuck on a layout it cannot reach")

		expectRolledOut(storedRV(ctx, cl, "rv-1"), newConfiguration())
		expectQueued(storedRV(ctx, cl, "rv-2"), r2Configuration())
		expectQueued(storedRV(ctx, cl, "rv-3"), r2Configuration())
	})
})
