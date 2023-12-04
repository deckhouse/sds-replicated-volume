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

	"github.com/go-logr/logr"
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
)

const (
	LinstorLeaderControllerName    = "linstor-leader-controller"
	LinstorLeaderLabel             = "storage.deckhouse.io/linstor-leader"
	LinstorControllerAppLabelValue = "linstor-controller"
)

func NewLinstorLeader(
	ctx context.Context,
	mgr manager.Manager,
	linstorLeaseName string,
	interval int,
) (controller.Controller, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger()

	c, err := controller.New(LinstorLeaderControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {

			// fmt.Println("START EVENT ", request)

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
		return nil, err
	}

	err = c.Watch(
		source.Kind(mgr.GetCache(), &coordinationv1.Lease{}),
		handler.Funcs{
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				if request.Name == linstorLeaseName {
					log.Info("Start of CREATE event of leases.coordination.k8s.io resource with name: " + request.Name)
					err := reconcileLinstorControllerPods(ctx, cl, log, request.Namespace, linstorLeaseName)
					if err != nil {
						log.Error(err, fmt.Sprintf("error in reconcileLinstorControllerPods. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}

					log.Info("END of CREATE event of leases.coordination.k8s.io resource with name: " + request.Name)
				}

			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {

				if e.ObjectNew.GetName() == linstorLeaseName {
					newLease, ok := e.ObjectNew.(*coordinationv1.Lease)
					if !ok {
						log.Error(err, "error get  ObjectNew Lease with name: "+e.ObjectNew.GetName())
					}

					oldLease, ok := e.ObjectOld.(*coordinationv1.Lease)
					if !ok {
						log.Error(err, "error get  ObjectOld Lease with name: "+e.ObjectOld.GetName())
					}

					var oldIdentity, newIdentity string

					if oldLease.Spec.HolderIdentity != nil {
						oldIdentity = *oldLease.Spec.HolderIdentity
					} else {
						oldIdentity = "nil"
					}

					if newLease.Spec.HolderIdentity != nil {
						newIdentity = *newLease.Spec.HolderIdentity
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
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {},
		})

	if err != nil {
		return nil, err
	}

	return c, err

}

func reconcileLinstorControllerPods(ctx context.Context, cl client.Client, log logr.Logger, linstorNamespace, linstorLeaseName string) error {
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
