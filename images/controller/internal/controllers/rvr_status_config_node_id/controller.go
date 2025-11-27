package rvrstatusconfignodeid

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
		Named("rvr_status_config_node_id_controller").
		For(&v1alpha3.ReplicatedVolumeReplica{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(ce event.CreateEvent) bool {
				rvr, ok := ce.Object.(*v1alpha3.ReplicatedVolumeReplica)
				if !ok {
					return false
				}
				// Trigger only if nodeID is not set
				return rvr.Status == nil || rvr.Status.Config == nil || rvr.Status.Config.NodeId == nil
			},
			UpdateFunc: func(_ event.UpdateEvent) bool {
				// No-op: nodeID is immutable once set, so we only care about CREATE
				return false
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				// No-op: deletion doesn't require nodeID assignment
				return false
			},
			GenericFunc: func(ge event.GenericEvent) bool {
				rvr, ok := ge.Object.(*v1alpha3.ReplicatedVolumeReplica)
				if !ok {
					return false
				}
				// Trigger only if nodeID is not set (for reconciliation on startup)
				return rvr.Status == nil || rvr.Status.Config == nil || rvr.Status.Config.NodeId == nil
			},
		}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.Log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
