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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"reflect"
	"sds-replicated-volume-controller/api/linstor"
	"sds-replicated-volume-controller/pkg/logger"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strconv"
	"time"
)

const (
	linstorPortRangeWatcherCtrlName = "linstor-port-range-watcher-controller"
	linstorPortRangeConfigMapName   = "linstor-port-range"
	propContainerName               = "d2ef39f4afb6fbe91ab4c9048301dc4826d84ed221a5916e92fa62fdb99deef0"
	propKey                         = "TcpPortAutoRange"
	propInstance                    = "/CTRLCFG"
)

func NewLinstorPortRangeWatcher(
	mgr manager.Manager,
	interval int,
	log logger.Logger,
) (controller.Controller, error) {
	cl := mgr.GetClient()

	c, err := controller.New(linstorPortRangeWatcherCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			if request.Name == linstorPortRangeConfigMapName {
				log.Info("START reconcile of Linstor port range configmap with name: " + request.Name)

				shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileConfigMapEvent. Add to retry after %d seconds.", interval))
					return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(interval) * time.Second}, nil
				}

				log.Info("END reconcile of Linstor port range configmap with name: " + request.Name)
			}

			return reconcile.Result{Requeue: false}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(
		source.Kind(mgr.GetCache(), &corev1.ConfigMap{}),
		handler.Funcs{
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				if e.Object.GetName() == linstorPortRangeConfigMapName {
					log.Info("START from CREATE reconcile of ConfigMap with name: " + e.Object.GetName())
					request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}

					shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, request, log)
					if shouldRequeue {
						log.Error(err, fmt.Sprintf("error in ReconcileConfigMapEvent. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}

					log.Info("END from CREATE reconcile of ConfigMap with name: " + request.Name)
				}

			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				if e.ObjectNew.GetName() == linstorPortRangeConfigMapName {
					newCM, ok := e.ObjectNew.(*corev1.ConfigMap)
					if !ok {
						log.Error(err, "error get ObjectNew ConfigMap")
					}

					oldCM, ok := e.ObjectOld.(*corev1.ConfigMap)
					if !ok {
						log.Error(err, "error get ObjectOld ConfigMap")
					}

					if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(newCM.Data, oldCM.Data) {
						log.Info("START from UPDATE reconcile of ConfigMap with name: " + e.ObjectNew.GetName())
						request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
						shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, request, log)
						if shouldRequeue {
							log.Error(err, fmt.Sprintf("error in ReconcileConfigMapEvent. Add to retry after %d seconds.", interval))
							q.AddAfter(request, time.Duration(interval)*time.Second)
						}
						log.Info("END from UPDATE reconcile of ConfigMap with name: " + e.ObjectNew.GetName())
					}
				}
			},
		})
	if err != nil {
		return nil, err
	}
	return c, err
}

func ReconcileConfigMapEvent(ctx context.Context, cl client.Client, request reconcile.Request, log logger.Logger) (bool, error) {
	configMap := &corev1.ConfigMap{}
	err := cl.Get(ctx, request.NamespacedName, configMap)
	if err != nil {
		return true, err
	}

	minPort := configMap.Data["minPort"]
	maxPort := configMap.Data["maxPort"]

	minPortInt, err := strconv.Atoi(minPort)
	if err != nil {
		return true, err
	}
	maxPortInt, err := strconv.Atoi(maxPort)
	if err != nil {
		return true, err
	}

	if maxPortInt < minPortInt {
		return false, fmt.Errorf("range start port %d is less than range end port %d", minPortInt, maxPortInt)
	}

	if maxPortInt > 65535 {
		return false, fmt.Errorf("range end port %d must be less then 65535", maxPortInt)
	}

	if maxPortInt < 1024 {
		return false, fmt.Errorf("range start port %d must be more then 1024", minPortInt)
	}

	objs := linstor.PropsContainersList{}
	log.Info(fmt.Sprintf("Retrieveng list of prop containers %v", cl.List(ctx, &objs, &client.ListOptions{})))
	propContainerExists := false

	log.Info("Checking port range in crd")

	for _, item := range objs.Items {
		if item.Name == propContainerName {
			propContainerExists = true
			if item.Spec.PropValue != fmt.Sprintf("%s-%s", minPort, maxPort) {
				item.Spec.PropValue = fmt.Sprintf("%s-%s", minPort, maxPort)
				err = cl.Update(ctx, &item, &client.UpdateOptions{})
				if err != nil {
					return true, err
				}
				log.Info(fmt.Sprintf("Item updated to %s-%s", minPort, maxPort))
			}
		}
	}

	if propContainerExists == false {
		err = cl.Create(ctx, &linstor.PropsContainers{
			ObjectMeta: metav1.ObjectMeta{
				Name: propContainerName,
			},
			Spec: linstor.PropsContainersSpec{
				PropKey:       propKey,
				PropValue:     fmt.Sprintf("%s-%s", minPort, maxPort),
				PropsInstance: propInstance,
			},
		})
		if err != nil {
			return true, err
		}
		log.Info(fmt.Sprintf("Item created with range %s-%s", minPort, maxPort))
	}

	return false, nil
}
