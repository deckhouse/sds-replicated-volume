package rvstatusconfigdeviceminor

import (
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

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
		Named("rv_status_config_device_minor_controller").
		For(&v1alpha3.ReplicatedVolume{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(ce event.CreateEvent) bool {
				rv, ok := ce.Object.(*v1alpha3.ReplicatedVolume)
				if !ok {
					return false
				}
				// Trigger only if Config doesn't exist (deviceMinor is not set)
				// Note: Since DeviceMinor is uint (not *uint), we can't check for nil.
				// We consider deviceMinor set if Config exists (even if value is 0, as 0 is a valid deviceMinor).
				return rv.Status == nil || rv.Status.Config == nil
			},
			UpdateFunc: func(ue event.UpdateEvent) bool {
				rv, ok := ue.ObjectNew.(*v1alpha3.ReplicatedVolume)
				if !ok {
					return false
				}
				// Trigger only if Config doesn't exist (deviceMinor is not set)
				// Note: Since DeviceMinor is uint (not *uint), we can't check for nil.
				// We consider deviceMinor set if Config exists (even if value is 0, as 0 is a valid deviceMinor).
				return rv.Status == nil || rv.Status.Config == nil
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				// No-op: deletion doesn't require deviceMinor assignment
				return false
			},
			GenericFunc: func(ge event.GenericEvent) bool {
				rv, ok := ge.Object.(*v1alpha3.ReplicatedVolume)
				if !ok {
					return false
				}
				// Trigger only if Config doesn't exist (for reconciliation on startup)
				// Note: Since DeviceMinor is uint (not *uint), we can't check for nil.
				// We consider deviceMinor set if Config exists (even if value is 0, as 0 is a valid deviceMinor).
				return rv.Status == nil || rv.Status.Config == nil
			},
		}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.Log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
