package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"

	. "github.com/deckhouse/sds-common-lib/utils"

	nodecfgv1alpha1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv"
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
) error {
	// Field indexers for cache queries by node and volume name
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha2.ReplicatedVolumeReplica{},
		"spec.nodeName",
		func(o client.Object) []string {
			r := o.(*v1alpha2.ReplicatedVolumeReplica)
			return []string{r.Spec.NodeName}
		},
	); err != nil {
		return LogError(log, fmt.Errorf("indexing spec.nodeName: %w", err))
	}

	// Field indexer for LVG by node name
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&nodecfgv1alpha1.LVMVolumeGroup{},
		"spec.local.nodeName",
		func(o client.Object) []string {
			lvg := o.(*nodecfgv1alpha1.LVMVolumeGroup)
			return []string{lvg.Spec.Local.NodeName}
		},
	); err != nil {
		return LogError(log, fmt.Errorf("indexing LVG spec.local.nodeName: %w", err))
	}
	type TReq = rv.Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("replicatedVolume").
		Watches(
			&v1alpha2.ReplicatedVolume{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("CreateFunc", "name", ce.Object.GetName())
					typedObj := ce.Object.(*v1alpha2.ReplicatedVolume)
					q.Add(rv.ResourceReconcileRequest{Name: typedObj.Name})
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("UpdateFunc", "name", ue.ObjectNew.GetName())
					typedObjOld := ue.ObjectOld.(*v1alpha2.ReplicatedVolume)
					typedObjNew := ue.ObjectNew.(*v1alpha2.ReplicatedVolume)

					// handle deletion: when deletionTimestamp is set, enqueue delete request
					if typedObjNew.DeletionTimestamp != nil {
						q.Add(rv.ResourceDeleteRequest{
							Name: typedObjNew.Name,
						})
						return
					}

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
					log.Debug("DeleteFunc - noop", "name", de.Object.GetName())
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					log.Debug("GenericFunc", "name", ge.Object.GetName())
				},
			}).
		Complete(rv.NewReconciler(log, mgr.GetClient(), mgr.GetAPIReader()))

	if err != nil {
		return LogError(log, fmt.Errorf("building controller: %w", err))
	}

	if err := mgr.Start(ctx); err != nil {
		return LogError(log, fmt.Errorf("starting controller: %w", err))
	}

	return ctx.Err()
}
