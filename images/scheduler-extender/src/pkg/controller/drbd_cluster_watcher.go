package controller

import (
	"context"
	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/logger"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func RunDRBDClusterWatcher(
	mgr manager.Manager,
	log logger.Logger,
	cacheMrg *cache.CacheManager,
) error {
	log.Info("[RunDRBDClusterWatcher] starts the work")

	c, err := controller.New("test-pvc-watcher", mgr, controller.Options{
		Reconciler: reconcile.Func(func(_ context.Context, _ reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
	})
	if err != nil {
		log.Error(err, "[RunDRBDClusterWatcher] unable to create controller")
		return err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &srv.DRBDCluster{}, handler.TypedFuncs[*srv.DRBDCluster, reconcile.Request]{
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[*srv.DRBDCluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			
		},
	}))

	return nil
}
