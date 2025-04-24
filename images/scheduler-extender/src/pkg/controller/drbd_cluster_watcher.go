package controller

import (
	"context"
	"fmt"
	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/consts"
	"scheduler-extender/pkg/logger"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	cacheMgr *cache.CacheManager,
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
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*srv.DRBDCluster], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newDrbdCluster := e.ObjectNew

			// TODO check DRBDCluster status, return if it is not ready yet
			if false {
				// some logic
			}

			pvc := &corev1.PersistentVolumeClaim{}
			err := mgr.GetClient().Get(ctx, client.ObjectKey{Namespace: "", Name: newDrbdCluster.Name}, pvc)
			if err != nil {
				log.Error(err, fmt.Sprintf("failed to get a pvc %s", pvc.Name))
				return
			}

			sc := &storagev1.StorageClass{}
			err = mgr.GetClient().Get(ctx, client.ObjectKey{Name: *pvc.Spec.StorageClassName}, sc)
			if err != nil {
				log.Error(err, fmt.Sprintf("failed to get a storage class %s", *pvc.Spec.StorageClassName))
				return
			}

			err = cacheMgr.RemoveSpaceReservationForPVCWithSelectedNode(pvc, sc.Parameters[consts.LvmTypeParamKey])
			if err != nil {
				// TODO implement retries in case of a failure
				log.Error(err, fmt.Sprintf("failed remove space reservation for pvc %s", pvc.Name))
				return
			}

		},
	}))

	return nil
}
