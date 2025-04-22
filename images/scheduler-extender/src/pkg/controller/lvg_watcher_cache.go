package controller

import (
	"context"
	"fmt"
	"reflect"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/logger"
)

const (
	LVGWatcherCacheCtrlName = "lvg-watcher-cache-controller"
)

func RunLVGWatcherCacheController(
	mgr manager.Manager,
	log logger.Logger,
	cacheMgr *cache.CacheManager,
) (controller.Controller, error) {
	log.Info("[RunLVGWatcherCacheController] starts the work")

	c, err := controller.New(LVGWatcherCacheCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(_ context.Context, _ reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
	})
	if err != nil {
		log.Error(err, "[RunCacheWatcherController] unable to create a controller")
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &snc.LVMVolumeGroup{}, handler.TypedFuncs[*snc.LVMVolumeGroup, reconcile.Request]{
		CreateFunc: func(_ context.Context, e event.TypedCreateEvent[*snc.LVMVolumeGroup], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info(fmt.Sprintf("[RunLVGWatcherCacheController] CreateFunc starts the cache reconciliation for the LVMVolumeGroup %s", e.Object.GetName()))

			lvg := e.Object
			if lvg.DeletionTimestamp != nil {
				log.Info(fmt.Sprintf("[RunLVGWatcherCacheController] the LVMVolumeGroup %s should not be reconciled", lvg.Name))
				return
			}

			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] tries to get the LVMVolumeGroup %s from the cache", lvg.Name))
			existedLVG := cacheMgr.TryGetLVG(lvg.Name)
			if existedLVG != nil {
				log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] the LVMVolumeGroup %s was found in the cache. It will be updated", lvg.Name))
				err := cacheMgr.UpdateLVG(lvg)
				if err != nil {
					log.Error(err, fmt.Sprintf("[RunLVGWatcherCacheController] unable to update the LVMVolumeGroup %s in the cache", lvg.Name))
				} else {
					log.Info(fmt.Sprintf("[RunLVGWatcherCacheController] cache was updated for the LVMVolumeGroup %s", lvg.Name))
				}
			} else {
				log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] the LVMVolumeGroup %s was not found. It will be added to the cache", lvg.Name))
				cacheMgr.AddLVG(lvg)
				log.Info(fmt.Sprintf("[RunLVGWatcherCacheController] cache was added for the LVMVolumeGroup %s", lvg.Name))
			}

			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] starts to clear the cache for the LVMVolumeGroup %s", lvg.Name))
			pvcs, err := cacheMgr.GetAllPVCForLVG(lvg.Name)
			if err != nil {
				log.Error(err, fmt.Sprintf("[RunLVGWatcherCacheController] unable to get all PVC for the LVMVolumeGroup %s", lvg.Name))
				return
			}

			for _, pvc := range pvcs {
				log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] cached PVC %s/%s belongs to LVMVolumeGroup %s", pvc.Namespace, pvc.Name, lvg.Name))
				log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s has status phase %s", pvc.Namespace, pvc.Name, pvc.Status.Phase))
				if pvc.Status.Phase == v1.ClaimBound {
					log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] cached PVC %s/%s has Status.Phase Bound. It will be removed from the cache for LVMVolumeGroup %s", pvc.Namespace, pvc.Name, lvg.Name))
					cacheMgr.RemovePVCFromTheCache(pvc)

					log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s was removed from the cache for LVMVolumeGroup %s", pvc.Namespace, pvc.Name, lvg.Name))
				}
			}

			log.Info(fmt.Sprintf("[RunLVGWatcherCacheController] cache for the LVMVolumeGroup %s was reconciled by CreateFunc", lvg.Name))
		},
		UpdateFunc: func(_ context.Context, e event.TypedUpdateEvent[*snc.LVMVolumeGroup], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info(fmt.Sprintf("[RunCacheWatcherController] UpdateFunc starts the cache reconciliation for the LVMVolumeGroup %s", e.ObjectNew.GetName()))
			oldLvg := e.ObjectOld
			newLvg := e.ObjectNew
			err := cacheMgr.UpdateLVG(newLvg)
			if err != nil {
				log.Error(err, fmt.Sprintf("[RunLVGWatcherCacheController] unable to update the LVMVolumeGroup %s cache", newLvg.Name))
				return
			}
			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] successfully updated the LVMVolumeGroup %s in the cache", newLvg.Name))

			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] starts to calculate the size difference for LVMVolumeGroup %s", newLvg.Name))
			log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] old state LVMVolumeGroup %s has size %s", oldLvg.Name, oldLvg.Status.AllocatedSize.String()))
			log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] new state LVMVolumeGroup %s has size %s", newLvg.Name, newLvg.Status.AllocatedSize.String()))

			if !shouldReconcileLVG(oldLvg, newLvg) {
				log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] the LVMVolumeGroup %s should not be reconciled", newLvg.Name))
				return
			}
			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] the LVMVolumeGroup %s should be reconciled by Update Func", newLvg.Name))

			cachedPVCs, err := cacheMgr.GetAllPVCForLVG(newLvg.Name)
			if err != nil {
				log.Error(err, fmt.Sprintf("[RunLVGWatcherCacheController] unable to get all PVC for the LVMVolumeGroup %s", newLvg.Name))
				return
			}

			for _, pvc := range cachedPVCs {
				log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s from the cache belongs to LVMVolumeGroup %s", pvc.Namespace, pvc.Name, newLvg.Name))
				log.Trace(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s has status phase %s", pvc.Namespace, pvc.Name, pvc.Status.Phase))
				if pvc.Status.Phase == v1.ClaimBound {
					log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s from the cache has Status.Phase Bound. It will be removed from the reserved space in the LVMVolumeGroup %s", pvc.Namespace, pvc.Name, newLvg.Name))
					cacheMgr.RemovePVCFromTheCache(pvc)
					log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] PVC %s/%s was removed from the LVMVolumeGroup %s in the cache", pvc.Namespace, pvc.Name, newLvg.Name))
				}
			}

			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] Update Func ends reconciliation the LVMVolumeGroup %s cache", newLvg.Name))
		},
		DeleteFunc: func(_ context.Context, e event.TypedDeleteEvent[*snc.LVMVolumeGroup], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Info(fmt.Sprintf("[RunCacheWatcherController] DeleteFunc starts the cache reconciliation for the LVMVolumeGroup %s", e.Object.GetName()))
			lvg := e.Object
			cacheMgr.DeleteLVG(lvg.Name)
			log.Debug(fmt.Sprintf("[RunLVGWatcherCacheController] LVMVolumeGroup %s was deleted from the cache", lvg.Name))
		},
	},
	),
	)
	if err != nil {
		log.Error(err, "[RunCacheWatcherController] unable to watch the events")
		return nil, err
	}

	return c, nil
}

func shouldReconcileLVG(oldLVG, newLVG *snc.LVMVolumeGroup) bool {
	if newLVG.DeletionTimestamp != nil {
		return false
	}

	if oldLVG.Status.AllocatedSize.Value() == newLVG.Status.AllocatedSize.Value() &&
		reflect.DeepEqual(oldLVG.Status.ThinPools, newLVG.Status.ThinPools) {
		return false
	}

	return true
}
