/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"drbd-cluster-sync/config"
	"drbd-cluster-sync/logger"

	v1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	kubecl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	lapi "github.com/deckhouse/sds-replicated-volume/api/linstor"
	srv2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
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
			pvList := &v1.PersistentVolumeList{}
			if err := kc.List(ctx, pvList); err != nil {
				log.Error(err, "[RunLayerResourceIDsWatcher] failed to get persistent volumes")
				return
			}

			pvMap := make(map[string]*v1.PersistentVolume, len(pvList.Items))
			for _, pv := range pvList.Items {
				pvMap[pv.Name] = &pv
			}
			log.Trace("[RunLayerResourceIDsWatcher] PV map", "map", marshalLog(pvMap))

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
			log.Trace("[RunLayerResourceIDsWatcher] lri Map", "map", marshalLog(lriMap))

			replicaMap := make(map[string]*srv2.DRBDResourceReplica)
			for _, lsv := range layerStorageVolumeList.Items {
				lri, found := lriMap[lsv.Spec.LayerResourceID]
				if !found {
					fmt.Printf("[RunLayerResourceIDsWatcher] no layer resourceid %d found. skipping iteration", lsv.Spec.LayerResourceID)
				}

				isDiskless := false
				if lsv.Spec.ProviderKind == "DISKLESS" {
					isDiskless = true
				}
				nodeName := strings.ToLower(lsv.Spec.NodeName)

				replica, found := replicaMap[lri.Spec.ResourceName]
				if !found {
					pvName := strings.ToLower(lri.Spec.ResourceName)

					replicaMap[lri.Spec.ResourceName] = &srv2.DRBDResourceReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: pvName,
							// Namespace: pvMap[pvName].Spec.ClaimRef.Namespace,
							Namespace: "default",
						},
						Spec: srv2.DRBDResourceReplicaSpec{
							Peers: map[string]srv2.Peer{
								nodeName: {
									Diskless: isDiskless,
								},
							},
						},
					}
					continue
				}

				peer := replica.Spec.Peers[nodeName]
				peer.Diskless = isDiskless
				replica.Spec.Peers[nodeName] = peer
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
			existingResource := &srv2.DRBDResourceReplica{}
			err := kc.Get(ctx, kubecl.ObjectKey{
				Namespace: drbdResourceReplica.Namespace,
				Name:      drbdResourceReplica.Name,
			}, existingResource)

			if k8sErr.IsNotFound(err) {
				err := kc.Create(ctx, drbdResourceReplica)
				if err == nil {
					log.Info(fmt.Sprintf("[RunLayerResourceIDsWatcher] DRBD resource replica %s successfully created", drbdResourceReplica.Name))
					return err
				}
				return nil
			}

			drbdResourceReplica.ResourceVersion = existingResource.ResourceVersion
			err = kc.Update(ctx, drbdResourceReplica)
			if err == nil {
				log.Info(fmt.Sprintf("[RunLayerResourceIDsWatcher] DRBD resource replica %s successfully updated", drbdResourceReplica.Name))
			}
			return err
		},
	); err != nil {
		log.Error(err, fmt.Sprintf("[RunLayerResourceIDsWatcher] failed to create a DRBD resource replica %s", drbdResourceReplica.Name))
	}
}

func marshalLog(data any) string {
	bytes, _ := json.MarshalIndent(data, "", "   ")
	return string(bytes)
}
