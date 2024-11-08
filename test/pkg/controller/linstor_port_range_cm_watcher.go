/*
Copyright 2024 Flant JSC

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
	"reflect"
	"strconv"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	"github.com/deckhouse/sds-replicated-volume/api/linstor"
	corev1 "k8s.io/api/core/v1"
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
	linstorPortRangeWatcherCtrlName = "linstor-port-range-watcher-controller"
	linstorPortRangeConfigMapName   = "linstor-port-range"
	linstorPropName                 = "d2ef39f4afb6fbe91ab4c9048301dc4826d84ed221a5916e92fa62fdb99deef0"
	linstorTCPPortAutoRangeKey      = "TcpPortAutoRange"

	incorrectPortRangeKey = "storage.deckhouse.io/incorrect-port-range"
	minPortKey            = "minPort"
	minPortValue          = 1024
	maxPortKey            = "maxPort"
	maxPortValue          = 65535
)

func NewLinstorPortRangeWatcher(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
	log logger.Logger,
) error {
	cl := mgr.GetClient()

	c, err := controller.New(linstorPortRangeWatcherCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			if request.Name == linstorPortRangeConfigMapName {
				log.Info("START reconcile of Linstor port range configmap with name: " + request.Name)

				shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, lc, request, log)
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
		return err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{}, &handler.TypedFuncs[*corev1.ConfigMap, reconcile.Request]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*corev1.ConfigMap], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.Object.GetName() == linstorPortRangeConfigMapName {
				log.Info("START from CREATE reconcile of ConfigMap with name: " + e.Object.GetName())
				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}

				shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, lc, request, log)
				if shouldRequeue {
					log.Error(err, fmt.Sprintf("error in ReconcileConfigMapEvent. Add to retry after %d seconds.", interval))
					q.AddAfter(request, time.Duration(interval)*time.Second)
				}

				log.Info("END from CREATE reconcile of ConfigMap with name: " + request.Name)
			}
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*corev1.ConfigMap], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.ObjectNew.GetName() == linstorPortRangeConfigMapName {
				if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(e.ObjectNew.Data, e.ObjectOld.Data) {
					log.Info("START from UPDATE reconcile of ConfigMap with name: " + e.ObjectNew.GetName())
					request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
					shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, lc, request, log)
					if shouldRequeue {
						log.Error(err, fmt.Sprintf("error in ReconcileConfigMapEvent. Add to retry after %d seconds.", interval))
						q.AddAfter(request, time.Duration(interval)*time.Second)
					}
					log.Info("END from UPDATE reconcile of ConfigMap with name: " + e.ObjectNew.GetName())
				}
			}
		},
	}))
	if err != nil {
		return err
	}
	return err
}

func updateConfigMapLabel(ctx context.Context, cl client.Client, configMap *corev1.ConfigMap, value string) error {
	if configMap.Labels == nil {
		configMap.Labels = make(map[string]string)
	}

	configMap.Labels[incorrectPortRangeKey] = value
	return cl.Update(ctx, configMap)
}

func ReconcileConfigMapEvent(ctx context.Context, cl client.Client, lc *lapi.Client, request reconcile.Request, log logger.Logger) (bool, error) {
	configMap := &corev1.ConfigMap{}
	err := cl.Get(ctx, request.NamespacedName, configMap)
	if err != nil {
		return true, err
	}

	minPort := configMap.Data[minPortKey]
	maxPort := configMap.Data[maxPortKey]

	minPortInt, err := strconv.Atoi(minPort)
	if err != nil {
		return false, err
	}
	maxPortInt, err := strconv.Atoi(maxPort)
	if err != nil {
		return false, err
	}

	if maxPortInt < minPortInt {
		err = updateConfigMapLabel(ctx, cl, configMap, "true")
		if err != nil {
			return true, err
		}
		log.Error(err, fmt.Sprintf("range start port %d is less than range end port %d", minPortInt, maxPortInt))
		return false, fmt.Errorf("range start port %d is less than range end port %d", minPortInt, maxPortInt)
	}

	if maxPortInt > maxPortValue {
		err = updateConfigMapLabel(ctx, cl, configMap, "true")
		if err != nil {
			return true, err
		}
		log.Error(err, fmt.Sprintf("range end port %d must be less then %d", maxPortInt, maxPortValue))
		return false, fmt.Errorf("range end port %d must be less then %d", maxPortInt, maxPortValue)
	}

	if minPortInt < minPortValue {
		err := updateConfigMapLabel(ctx, cl, configMap, "true")
		if err != nil {
			return true, err
		}
		log.Error(err, fmt.Sprintf("range start port %d must be more then %d", minPortInt, minPortValue))
		return false, fmt.Errorf("range start port %d must be more then %d", minPortInt, minPortValue)
	}

	err = updateConfigMapLabel(ctx, cl, configMap, "false")
	if err != nil {
		return true, err
	}

	log.Info("Checking controller port range")
	kvObjs, err := lc.Controller.GetProps(ctx)
	if err != nil {
		return true, err
	}

	for kvKey, kvItem := range kvObjs {
		if kvKey != linstorTCPPortAutoRangeKey {
			continue
		}

		portRange := fmt.Sprintf("%d-%d", minPortInt, maxPortInt)

		if kvItem != portRange {
			log.Info(fmt.Sprintf("Current port range %s, actual %s", kvItem, portRange))
			err = lc.Controller.Modify(ctx, lapi.GenericPropsModify{
				OverrideProps: map[string]string{
					linstorTCPPortAutoRangeKey: portRange}})
			if err != nil {
				return true, err
			}
			propObject := linstor.PropsContainers{}
			err = cl.Get(ctx, types.NamespacedName{Namespace: "default",
				Name: linstorPropName}, &propObject)
			if err != nil {
				return true, err
			}

			log.Info(fmt.Sprintf("Check port range in CR. %s, actual %s",
				propObject.Spec.PropValue,
				portRange))
			if propObject.Spec.PropValue != portRange {
				propObject.Spec.PropValue = portRange
				err = cl.Update(ctx, &propObject)
				if err != nil {
					return true, err
				}
				log.Info(fmt.Sprintf("port range in CR updated to %s", portRange))
			}
		}
	}

	return false, nil
}
