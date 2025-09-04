package main

//lint:file-ignore ST1001 utils is the only exception

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	. "github.com/deckhouse/sds-common-lib/utils"
	"golang.org/x/time/rate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile/rvr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
	type TReq = rvr.Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	// max(...)
	rl := workqueue.NewTypedMaxOfRateLimiter(
		// per_item retries: min(5ms*2^<num-failures>, 30s)
		// Default was: 5*time.Millisecond, 1000*time.Second
		workqueue.NewTypedItemExponentialFailureRateLimiter[TReq](5*time.Millisecond, 30*time.Second),
		// overall retries: 5 qps, 30 burst size. This is only for retry speed and its only the overall factor (not per item)
		// Default was: rate.Limit(10), 100
		&workqueue.TypedBucketRateLimiter[TReq]{Limiter: rate.NewLimiter(rate.Limit(5), 30)},
	)

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("replicatedVolumeReplica").
		WithOptions(controller.TypedOptions[TReq]{
			RateLimiter: rl,
		}).
		Watches(
			&v1alpha2.ReplicatedVolumeReplica{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("CreateFunc", "name", ce.Object.GetName())
					obj := ce.Object.(*v1alpha2.ReplicatedVolumeReplica)

					if obj.DeletionTimestamp != nil {
						log.Debug("CreateFunc -> ResourceDeleteRequest")

						q.Add(rvr.ResourceDeleteRequest{
							Name:                 obj.Name,
							ReplicatedVolumeName: obj.Spec.ReplicatedVolumeName,
						})
						return
					}

					// unfinished signals
					// TODO in admission webhook we should disallow creation of resources with "signal" annotations, so that current block only work for SYNCs
					if obj.Annotations[v1alpha2.AnnotationKeyPrimaryForce] != "" {
						log.Debug("CreateFunc -> ResourcePrimaryForceRequest")
						q.Add(rvr.ResourcePrimaryForceRequest{Name: obj.Name})
					}
					if obj.Annotations[v1alpha2.AnnotationKeyNeedResize] != "" {
						log.Debug("CreateFunc -> ResourceResizeRequest")
						q.Add(rvr.ResourceResizeRequest{Name: obj.Name})
					}

					q.Add(rvr.ResourceReconcileRequest{Name: obj.Name})
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					log.Debug("UpdateFunc", "name", ue.ObjectNew.GetName())
					objOld := ue.ObjectOld.(*v1alpha2.ReplicatedVolumeReplica)
					objNew := ue.ObjectNew.(*v1alpha2.ReplicatedVolumeReplica)

					// handle deletion: when deletionTimestamp is set, enqueue delete request
					if objNew.DeletionTimestamp != nil {
						q.Add(rvr.ResourceDeleteRequest{
							Name:                 objNew.Name,
							ReplicatedVolumeName: objNew.Spec.ReplicatedVolumeName,
						})
						return
					}

					// detect signals passed with annotations
					if annotationAdded(objOld, objNew, v1alpha2.AnnotationKeyPrimaryForce) {
						q.Add(rvr.ResourcePrimaryForceRequest{Name: objNew.Name})
					}
					if annotationAdded(objOld, objNew, v1alpha2.AnnotationKeyNeedResize) {
						q.Add(rvr.ResourceResizeRequest{Name: objNew.Name})
					}

					// skip status and metadata updates
					specChanged := objOld.Generation < objNew.Generation
					initialSync := initialSyncStatusChangedToTrue(objOld, objNew)

					if !specChanged && !initialSync {
						log.Debug(
							"UpdateFunc - irrelevant change, skip",
							"name", ue.ObjectNew.GetName(),
						)
						return
					}

					log.Debug("UpdateFunc - reconcile required",
						"specChanged", specChanged,
						"initialSync", initialSync,
					)

					q.Add(rvr.ResourceReconcileRequest{Name: objNew.Name})
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
					log.Debug("GenericFunc - noop", "name", ge.Object.GetName())
				},
			}).
		Complete(rvr.NewReconciler(log, mgr.GetClient(), nodeName))

	if err != nil {
		return LogError(log, fmt.Errorf("building controller: %w", err))
	}

	if err := mgr.Start(ctx); err != nil {
		return LogError(log, fmt.Errorf("starting controller: %w", err))
	}

	return ctx.Err()
}

func annotationAdded(
	oldObj *v1alpha2.ReplicatedVolumeReplica,
	newObj *v1alpha2.ReplicatedVolumeReplica,
	key string,
) bool {
	return oldObj.Annotations[key] == "" && newObj.Annotations[key] != ""
}

func initialSyncStatusChangedToTrue(
	oldObj *v1alpha2.ReplicatedVolumeReplica,
	newObj *v1alpha2.ReplicatedVolumeReplica,
) bool {
	return initialSyncTrue(newObj) && !initialSyncTrue(oldObj)
}

func initialSyncTrue(obj *v1alpha2.ReplicatedVolumeReplica) bool {
	return obj.Status != nil &&
		meta.IsStatusConditionTrue(
			obj.Status.Conditions,
			v1alpha2.ConditionTypeInitialSync,
		)
}
