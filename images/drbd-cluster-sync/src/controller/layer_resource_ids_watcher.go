package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	lapi "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"drbd-cluster-sync/config"
	"drbd-cluster-sync/logger"

	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
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
	kc kubecl.Client,
	opts *config.Options,
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
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*lapi.LayerResourceIds], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			pvs := &v1.PersistentVolumeList{}
			if err := kc.List(ctx, pvs); err != nil {
				log.Error(err, "[RunLayerResourceIDsWatcher] failed to get persistent volumes")
				return
			}

			pvcMap := make(map[string]*v1.PersistentVolume, len(pvs.Items))
			for _, pvc := range pvs.Items {
				pvcMap[pvc.Name] = &pvc
			}

			layerStorageVolumeList := &lapi.LayerStorageVolumesList{}
			err := kc.List(ctx, layerStorageVolumeList)
			if err != nil {
				log.Error(err, "[RunLayerResourceIDsWatcher] failed to list layer storage volumes")
				return
			}

			layerStorageResourceIDs := &lapi.LayerResourceIdsList{}
			err = kc.List(ctx, layerStorageResourceIDs)
			if err != nil {
				log.Error(err, "[RunLayerResourceIDsWatcher] failed to list layer resource id")
				return
			}

			lriMap := make(map[int]*lapi.LayerResourceIds, len(layerStorageResourceIDs.Items))
			for _, lri := range layerStorageResourceIDs.Items {
				lriMap[lri.Spec.LayerResourceID] = &lri
			}

			replicaMap := make(map[string]*srv2.DRBDResourceReplica)
			for _, lsv := range layerStorageVolumeList.Items {
				lri, found := lriMap[lsv.Spec.LayerResourceID]
				if !found {
					fmt.Printf("[RunLayerResourceIDsWatcher] no layer resource id %s found. skipping iteration")
				}

				isDiskless := false
				if lsv.Spec.ProviderKind == "DISKLESS" {
					isDiskless = true
				}
				nodeName := strings.ToLower(lsv.Spec.NodeName)

				r, found := replicaMap[lri.Spec.ResourceName]
				if !found {
					pvName := strings.ToLower(lri.Spec.ResourceName)

					replicaMap[lri.Spec.ResourceName] = &srv2.DRBDResourceReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pvName,
							Namespace: pvcMap[pvName].Spec.ClaimRef.Namespace,
						},
						Spec: srv2.DRBDResourceReplicaSpec{
							Peers: map[string]srv2.Peer{
								nodeName: srv2.Peer{
									Diskless: isDiskless,
								},
							},
						},
					}
					continue
				}

				peer := r.Spec.Peers[nodeName]
				peer.Diskless = isDiskless
				r.Spec.Peers[nodeName] = peer
			}

			var wg sync.WaitGroup
			semaphore := make(chan struct{}, opts.NumWorkers)
			for _, replica := range replicaMap {
				semaphore <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() {
						<-semaphore
						wg.Done()
					}()
					createDRBDResource(ctx, kc, replica, opts, log)
				}()
			}

			wg.Wait()
			close(semaphore)
			return
		},
	}))
	if err != nil {
		log.Error(err, "[RunLayerResourceIDsWatcher] Watch error")
		return err
	}

	return nil
}

func createDRBDResource(ctx context.Context, kc kubecl.Client, drbdResourceReplica *srv2.DRBDResourceReplica, opts *config.Options, log *logger.Logger) {
	if err := retry.OnError(
		// backoff settings
		wait.Backoff{
			Duration: 2 * time.Second,                   // initial delay before first retry
			Factor:   1.0,                               // Cap is multiplied by this value each retry
			Steps:    int(opts.RetryCount),              // amount of retries
			Cap:      time.Duration(opts.RetryDelaySec), // delay between retries
		},
		// this function takes an error returned by kc.Create and decides whether to make a retry or not
		func(err error) bool {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.Error(err, "[RunLayerResourceIDsWatcher] drbd resource replica retry context err")
				return false
			}

			if statusError, ok := err.(*k8sErr.StatusError); ok {
				switch statusError.ErrStatus.Reason {
				case
					metav1.StatusReasonForbidden,
					metav1.StatusReasonAlreadyExists,
					metav1.StatusReasonInvalid,
					metav1.StatusReasonConflict,
					metav1.StatusReasonBadRequest:
					log.Error(statusError, fmt.Sprintf("[RunLayerResourceIDsWatcher] drbd resource replica retry creation err: %s", statusError.ErrStatus.Reason))
					return false
				}
			}
			return true
		},
		func() error {
			err := kc.Create(ctx, drbdResourceReplica)
			if err == nil {
				log.Info(fmt.Sprintf("[RunLayerResourceIDsWatcher] DRBD resource replica %s successfully created", drbdResourceReplica.Name))
			}
			return err
		},
	); err != nil {
		log.Error(err, fmt.Sprintf("[RunLayerResourceIDsWatcher] failed to create a DRBD resource replica %s", drbdResourceReplica.Name))
	}
}
