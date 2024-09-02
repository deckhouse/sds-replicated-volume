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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"sds-replicated-volume-controller/pkg/logger"
)

const (
	storageClassAnnotationsCtrlName = "storage-class-annotations-controller"
	controllerConfigMapName         = "sds-replicated-volume-controller-config"
	// virtualizationModuleName        = "virtualization"
	virtualizationModuleEnabledKey = "virtualizationEnabled"
)

func NewStorageClassAnnotationsReconciler(
	mgr manager.Manager,
	interval int,
	log logger.Logger,
) error {
	cl := mgr.GetClient()

	c, err := controller.New(storageClassAnnotationsCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			log.Info(fmt.Sprintf("[storageClassAnnotationsReconciler] Get event for configmap %s/%s in reconciler", request.Namespace, request.Name))

			shouldRequeue, err := ReconcileControllerConfigMapEvent(ctx, cl, log, request)
			if shouldRequeue {
				log.Error(err, fmt.Sprintf("[storageClassAnnotationsReconciler] error in ReconcileControllerConfigMapEvent. Add to retry after %d seconds.", interval))
				return reconcile.Result{Requeue: true, RequeueAfter: time.Duration(interval) * time.Second}, nil
			}

			log.Info(fmt.Sprintf("[storageClassAnnotationsReconciler] Finish event for configmap %s/%s in reconciler", request.Namespace, request.Name))

			return reconcile.Result{Requeue: false}, nil
		}),
	})
	if err != nil {
		return err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{}, &handler.TypedFuncs[*corev1.ConfigMap, reconcile.Request]{
		CreateFunc: func(ctx context.Context, e event.TypedCreateEvent[*corev1.ConfigMap], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Trace(fmt.Sprintf("[storageClassAnnotationsReconciler] Get CREATE event for configmap %s/%s", e.Object.GetNamespace(), e.Object.GetName()))
			if e.Object.GetName() == controllerConfigMapName {
				log.Trace(fmt.Sprintf("[storageClassAnnotationsReconciler] configmap %s/%s is controller configmap. Add it to queue.", e.Object.GetNamespace(), e.Object.GetName()))
				request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.Object.GetNamespace(), Name: e.Object.GetName()}}
				q.Add(request)
			}
		},
		UpdateFunc: func(ctx context.Context, e event.TypedUpdateEvent[*corev1.ConfigMap], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log.Trace(fmt.Sprintf("[storageClassAnnotationsReconciler] Get UPDATE event for configmap %s/%s", e.ObjectNew.GetNamespace(), e.ObjectNew.GetName()))
			if e.ObjectNew.GetName() == linstorPortRangeConfigMapName {
				log.Trace(fmt.Sprintf("[storageClassAnnotationsReconciler] configmap %s/%s is controller configmap. Check if it was changed.", e.ObjectNew.GetNamespace(), e.ObjectNew.GetName()))
				if e.ObjectNew.GetDeletionTimestamp() != nil || !reflect.DeepEqual(e.ObjectNew.Data, e.ObjectOld.Data) {
					log.Trace(fmt.Sprintf("[storageClassAnnotationsReconciler] configmap %s/%s was changed. Add it to queue.", e.ObjectNew.GetNamespace(), e.ObjectNew.GetName()))
					request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: e.ObjectNew.GetNamespace(), Name: e.ObjectNew.GetName()}}
					q.Add(request)
				}
			}
		},
	}))
	if err != nil {
		return err
	}
	return err
}
