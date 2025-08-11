package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"

	. "github.com/deckhouse/sds-common-lib/u"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/sds-replicated-volume-controller/internal/reconcile/rv"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func runController(
	ctx context.Context,
	log *slog.Logger,
	mgr manager.Manager,
	nodeName string,
) error {
	type TReq = rv.Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("replicatedVolumeReplica").
		Watches(
			&v1alpha2.ReplicatedVolumeReplica{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("CreateFunc", "name", ce.Object.GetName())
					typedObj := ce.Object.(*v1alpha2.ReplicatedVolumeReplica)
					q.Add(rv.ResourceReconcileRequest{Name: typedObj.Name})
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("UpdateFunc", "name", ue.ObjectNew.GetName())
					typedObjOld := ue.ObjectOld.(*v1alpha2.ReplicatedVolumeReplica)
					typedObjNew := ue.ObjectNew.(*v1alpha2.ReplicatedVolumeReplica)

					// skip status and metadata updates
					if typedObjOld.Generation >= typedObjNew.Generation {
						log.Debug(
							"UpdateFunc - same generation, skip",
							"name", ue.ObjectNew.GetName(),
						)
						return
					}

					q.Add(rv.ResourceReconcileRequest{Name: typedObjNew.Name})
				},
				DeleteFunc: func(
					ctx context.Context,
					de event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					log.Debug("DeleteFunc", "name", de.Object.GetName())
					typedObj := de.Object.(*v1alpha2.ReplicatedVolumeReplica)
					q.Add(rv.ResourceDeleteRequest{
						Name:                 typedObj.Name,
						ReplicatedVolumeName: typedObj.Spec.ReplicatedVolumeName,
					})
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					log.Debug("GenericFunc", "name", ge.Object.GetName())
				},
			}).
		Complete(rv.NewReconciler(log, mgr.GetClient(), nodeName))

	if err != nil {
		return LogError(log, fmt.Errorf("building controller: %w", err))
	}

	if err := mgr.Start(ctx); err != nil {
		return LogError(log, fmt.Errorf("starting controller: %w", err))
	}

	return ctx.Err()
}
