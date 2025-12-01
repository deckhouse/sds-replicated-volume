package rvstatusconfigsharedsecret

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func BuildController(mgr manager.Manager) error {
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(RVStatusConfigSharedSecretControllerName).WithName("Reconciler"),
	)

	err := builder.ControllerManagedBy(mgr).
		Named(RVStatusConfigSharedSecretControllerName).
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica)
				if !ok {
					return nil
				}
				// Check if RVR has UnsupportedAlgorithm error
				// NOTE: Using EnqueueRequestsFromMapFunc is necessary because RVR doesn't have owner reference to RV.
				// They are linked via spec.replicatedVolumeName, so we need manual mapping.
				if !hasUnsupportedAlgorithmError(rvr) {
					return nil
				}
				// Map RVR to RV
				return []reconcile.Request{
					{NamespacedName: client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}},
				}
			}),
		).
		Complete(rec)

	if err != nil {
		return fmt.Errorf("building controller: %w", e.ErrUnknownf("%w", err))
	}

	return nil
}

// hasUnsupportedAlgorithmError checks if RVR has ConfigurationAdjusted=False with reason=UnsupportedAlgorithm
func hasUnsupportedAlgorithmError(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.Conditions == nil {
		return false
	}
	cfgAdj := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeConfigurationAdjusted)
	return cfgAdj != nil && cfgAdj.Status == metav1.ConditionFalse && cfgAdj.Reason == "UnsupportedAlgorithm"
}
