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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	return &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
		Type: v1alpha1.ConfigurationRolloutRollingUpdate,
		RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
			MaxParallel: 5,
		},
	}
}

// newRolloutRV builds an RV in normal operation (formation finished) with the given applied
// configuration state. A nil config means the volume never received a configuration.
func newRolloutRV(
	config *v1alpha1.ReplicatedVolumeConfiguration,
	appliedGeneration, observedGeneration int64,
) *v1alpha1.ReplicatedVolume {
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
