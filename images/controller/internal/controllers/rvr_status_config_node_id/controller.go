package rvrstatusconfignodeid

import (
	"context"
	"log/slog"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func BuildController(mgr manager.Manager) error {
	var rec = &Reconciler{
		cl:     mgr.GetClient(),
		rdr:    mgr.GetAPIReader(),
		sch:    mgr.GetScheme(),
		log:    slog.Default(),
		logAlt: mgr.GetLogger(),
	}

	type TReq = Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("rvr_status_config_node_id_controller").
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					_ context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					rvr, ok := ce.Object.(*v1alpha3.ReplicatedVolumeReplica)
					if !ok {
						return
					}

					// Trigger only if nodeID is not set
					if rvr.Status == nil || rvr.Status.Config == nil || rvr.Status.Config.NodeId == nil {
						req := AssignNodeIDRequest{Name: rvr.Name}
						q.Add(req)
					}
				},
				UpdateFunc: func(
					_ context.Context,
					_ event.TypedUpdateEvent[client.Object],
					_ TQueue,
				) {
					// No-op: nodeID is immutable once set, so we only care about CREATE
				},
				DeleteFunc: func(
					_ context.Context,
					_ event.TypedDeleteEvent[client.Object],
					_ TQueue,
				) {
					// No-op: deletion doesn't require nodeID assignment
				},
				GenericFunc: func(
					_ context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					rvr, ok := ge.Object.(*v1alpha3.ReplicatedVolumeReplica)
					if !ok {
						return
					}

					// Trigger only if nodeID is not set (for reconciliation on startup)
					if rvr.Status == nil || rvr.Status.Config == nil || rvr.Status.Config.NodeId == nil {
						req := AssignNodeIDRequest{Name: rvr.Name}
						q.Add(req)
					}
				},
			}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
