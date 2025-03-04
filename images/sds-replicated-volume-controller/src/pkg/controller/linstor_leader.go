/*
Copyright 2023 Flant JSC

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
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sds-replicated-volume-controller/pkg/logger"
)

const (
	LinstorLeaderControllerName    = "linstor-leader-controller"
	LinstorLeaderLabel             = "storage.deckhouse.io/linstor-leader"
	LinstorControllerAppLabelValue = "linstor-controller"
)

func NewLinstorLeader(
	mgr manager.Manager,
	linstorLeaseName string,
	interval int,
	log logger.Logger,
) error {
	cl := mgr.GetClient()

	c, err := controller.New(LinstorLeaderControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			if request.Name == linstorLeaseName {
				log.Info("Start reconcile of linstor-controller pods.")
				err := reconcileLinstorControllerPods(ctx, cl, log, request.Namespace, linstorLeaseName)
				if err != nil {
					log.Error(err, "Failed reconcile linstor-controller pods")
					return reconcile.Result{
						RequeueAfter: time.Duration(interval) * time.Second,
					}, nil
				}
				log.Info("Finish reconcile of linstor-controller pods.")
			}

			return reconcile.Result{Requeue: false}, nil
		}),
	})

	if err != nil {
		return err
	}

	err = c.Watch(
		source.Kind(mgr.GetCache(), &coordinationv1.Lease{}, &handler.TypedFuncs[*coordinationv1.Lease, reconcile.Request]{
			CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*coordinationv1.Lease], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				if request.Name == linstorLeaseName {
					log.Info("Start of CREATE event of leases.coordination.k8s.io resource with name: " + request.Name)
					err = reconcileLinstorControllerPods(ctx, cl, log, request.Namespace, linstorLeaseName)
					if err != nil {
						log.Error(err, fmt.Sprintf("error in reconcileLinstorControllerPods. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}

					log.Info("END of CREATE event of leases.coordination.k8s.io resource with name: " + request.Name)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*coordinationv1.Lease], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				if e.ObjectNew.GetName() == linstorLeaseName {
					var oldIdentity, newIdentity string

					if e.ObjectOld.Spec.HolderIdentity != nil {
						oldIdentity = *e.ObjectOld.Spec.HolderIdentity
					} else {
						oldIdentity = "nil"
					}

					if e.ObjectNew.Spec.HolderIdentity != nil {
						newIdentity = *e.ObjectNew.Spec.HolderIdentity
					} else {
						newIdentity = "nil"
					}

					if newIdentity != oldIdentity {
						log.Info("START from UPDATE event of leases.coordination.k8s.io with name: " + e.ObjectNew.GetName())
						log.Info("HolderIdentity changed from " + oldIdentity + " to " + newIdentity)
						request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
						err := reconcileLinstorControllerPods(ctx, cl, log, request.Namespace, linstorLeaseName)
						if err != nil {
							log.Error(err, fmt.Sprintf("error in reconcileLinstorControllerPods. Add to retry after %d seconds.", interval))
							q.AddAfter(request, time.Duration(interval)*time.Second)
						}
						log.Info("END from UPDATE event of leases.coordination.k8s.io with name: " + e.ObjectNew.GetName())
					}
				}
			},
		}))

	if err != nil {
		return err
	}

	return err
}

func reconcileLinstorControllerPods(ctx context.Context, cl client.Client, log logger.Logger, linstorNamespace, linstorLeaseName string) error {
	linstorLease := &coordinationv1.Lease{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      linstorLeaseName,
		Namespace: linstorNamespace,
	}, linstorLease)
	if err != nil {
		log.Error(err, "Failed get lease:"+linstorNamespace+"/"+linstorLeaseName)
		return err
	}

	if linstorLease.Spec.HolderIdentity != nil {
		log.Info("Leader pod name: " + *linstorLease.Spec.HolderIdentity)
	} else {
		log.Info("Leader pod name not set in Lease")
	}

	linstorControllerPods := &v1.PodList{}
	err = cl.List(ctx, linstorControllerPods, client.InNamespace(linstorNamespace), client.MatchingLabels{"app": LinstorControllerAppLabelValue})
	if err != nil {
		log.Error(err, "Failed get linstor-controller pods by label app="+LinstorControllerAppLabelValue)
		return err
	}

	for _, pod := range linstorControllerPods.Items {
		_, exists := pod.Labels[LinstorLeaderLabel]
		if exists {
			if linstorLease.Spec.HolderIdentity == nil || pod.Name != *linstorLease.Spec.HolderIdentity {
				log.Info("Remove leader label from pod: " + pod.Name)
				delete(pod.Labels, LinstorLeaderLabel)
				err := cl.Update(ctx, &pod)
				if err != nil {
					log.Error(err, "Failed update pod:"+pod.Namespace+"/"+pod.Name)
					return err
				}
			}
			continue
		}

		if linstorLease.Spec.HolderIdentity != nil && pod.Name == *linstorLease.Spec.HolderIdentity {
			log.Info("Set leader label to pod: " + pod.Name)
			if pod.Labels == nil {
				pod.Labels = make(map[string]string)
			}
			pod.Labels[LinstorLeaderLabel] = "true"
			err := cl.Update(ctx, &pod)
			if err != nil {
				log.Error(err, "Failed update pod:"+pod.Namespace+"/"+pod.Name)
				return err
			}
		}
	}

	return nil
}
