package controller

import (
	"context"
	"fmt"

	lapi "github.com/deckhouse/sds-replicated-volume/api/linstor"
	"k8s.io/client-go/util/workqueue"

	"drbd-cluster-sync/logger"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	LVGLayerResourceIDsWatcherName = "layer-resource-ids-watcher"
)

func RunLayerResourceIDsWatcher(
	mgr manager.Manager,
	log *logger.Logger,
) error {
	log.Info("[RunLayerResourceIDsWatcher] starts the work")

	c, err := controller.New(LVGLayerResourceIDsWatcherName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(_ context.Context, _ reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
	})
	if err != nil {
		log.Error(err, "[RunLayerResourceIDsWatcher] unable to create a controller")
		return err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &lapi.LayerResourceIds{}, handler.TypedFuncs[*lapi.LayerResourceIds, reconcile.Request]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[*lapi.LayerResourceIds], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info(fmt.Sprintf("[RunLayerResourceIDsWatcher] res id created %s", e.Object.GetName()))
		},
	}))

	return nil
}
