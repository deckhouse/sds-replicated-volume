package rvrstatusconfigaddress

import (
	"context"
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/agent/internal/errors"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {
	var rec = &Reconciler{
		cl:  mgr.GetClient(),
		rdr: mgr.GetAPIReader(),
		sch: mgr.GetScheme(),
		log: slog.Default(),
	}

	type TReq = Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("rvr_status_config_address_controller").
		Watches(
			&v1alpha3.ReplicatedVolume{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					// ...
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					// ...
				},
				DeleteFunc: func(
					ctx context.Context,
					de event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					// ...
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					// ...
				},
			}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
