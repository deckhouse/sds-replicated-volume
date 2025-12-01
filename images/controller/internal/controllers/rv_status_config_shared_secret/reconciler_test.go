package rvstatusconfigsharedsecret_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types" // cspell:words gomegatypes
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
)

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}

func parseQuantity(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvstatusconfigsharedsecret.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvstatusconfigsharedsecret.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		By("Reconciling non-existent ReplicatedVolume")
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("ReplicatedVolume created", func() {
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rv",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       parseQuantity("10Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
		})

		It("generates shared secret initially", func(ctx SpecContext) {
			By("Reconciling ReplicatedVolume without shared secret")
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).ToNot(Requeue(), "reconciliation should succeed")

			By("Verifying shared secret was generated")
			updatedRV := &v1alpha3.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
			Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Not(BeEmpty())), "shared secret should be set")
			Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(rvstatusconfigsharedsecret.AlgorithmSHA256)), "should use first algorithm (sha256)")

			By("Verifying condition was set")
			cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
			Expect(cond).NotTo(BeNil(), "condition should exist")
			Expect(cond.Status).To(Equal(metav1.ConditionTrue), "condition should be True")
			Expect(cond.Reason).To(Equal("AlgorithmSelected"), "reason should be AlgorithmSelected")
		})

		When("shared secret already set", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: rvstatusconfigsharedsecret.AlgorithmSHA256,
						},
					},
				}
			})

			When("no UnsupportedAlgorithm errors", func() {
				It("does nothing", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolume with shared secret and no errors")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Verifying nothing changed")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Equal("test-secret")), "shared secret should remain unchanged")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(rvstatusconfigsharedsecret.AlgorithmSHA256)), "algorithm should remain unchanged")
				})
			})

			When("UnsupportedAlgorithm error occurs", func() {
				var rvr *v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					rvr = &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-rvr",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-1",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Conditions: []metav1.Condition{
								{
									Type:               v1alpha3.ConditionTypeConfigurationAdjusted,
									Status:             metav1.ConditionFalse,
									Reason:             "UnsupportedAlgorithm",
									Message:            "Algorithm not supported",
									ObservedGeneration: 1,
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create ReplicatedVolumeReplica with error")
				})

				It("switches to next algorithm", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolume with UnsupportedAlgorithm error")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Verifying algorithm was switched to sha1")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(rvstatusconfigsharedsecret.AlgorithmSHA1)), "should switch to next algorithm (sha1)")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Not(Equal("test-secret"))), "shared secret should be regenerated")

					By("Verifying condition was updated")
					cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
					Expect(cond).NotTo(BeNil(), "condition should exist")
					Expect(cond.Status).To(Equal(metav1.ConditionTrue), "condition should be True")
				})

				When("last algorithm already used", func() {
					BeforeEach(func() {
						rv.Status.DRBD.Config.SharedSecretAlg = rvstatusconfigsharedsecret.AlgorithmSHA1 // Last algorithm
					})

					It("sets condition to False when all algorithms are exhausted", func(ctx SpecContext) {
						By("Reconciling ReplicatedVolume with last algorithm failed")
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).ToNot(Requeue(), "reconciliation should succeed")

						By("Verifying condition is set to False")
						updatedRV := &v1alpha3.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")

						cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
						Expect(cond).NotTo(BeNil(), "condition should exist")
						Expect(cond.Status).To(Equal(metav1.ConditionFalse), "condition should be False")
						Expect(cond.Reason).To(Equal("UnableToSelectSharedSecretAlgorithm"), "reason should indicate failure")
					})
				})
			})
		})
	})
})

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}
