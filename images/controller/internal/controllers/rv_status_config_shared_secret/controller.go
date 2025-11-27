package rvstatusconfigsharedsecret

import (
	"context"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func BuildController(mgr manager.Manager) error {
	var rec = &Reconciler{
		Cl:     mgr.GetClient(),
		Log:    slog.Default(),
		LogAlt: mgr.GetLogger(),
	}

	err := builder.ControllerManagedBy(mgr).
		Named("rv_status_config_shared_secret_controller").
		For(&v1alpha3.ReplicatedVolume{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(ce event.CreateEvent) bool {
				rv, ok := ce.Object.(*v1alpha3.ReplicatedVolume)
				if !ok {
					return false
				}
				// Trigger only if sharedSecret is not set
				return rv.Status == nil || rv.Status.Config == nil || rv.Status.Config.SharedSecret == ""
			},
			UpdateFunc: func(_ event.UpdateEvent) bool {
				// No-op: sharedSecret is immutable once set (unless algorithm fails)
				return false
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				// No-op: deletion doesn't require shared secret generation
				return false
			},
			GenericFunc: func(ge event.GenericEvent) bool {
				rv, ok := ge.Object.(*v1alpha3.ReplicatedVolume)
				if !ok {
					return false
				}
				// Trigger only if sharedSecret is not set (for reconciliation on startup)
				return rv.Status == nil || rv.Status.Config == nil || rv.Status.Config.SharedSecret == ""
			},
		}).
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
		return u.LogError(rec.Log, e.ErrUnknownf("building controller: %w", err))
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
