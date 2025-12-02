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

package rvrtiebreakercount

import (
	"context"
	"log/slog"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func BuildController(mgr manager.Manager) error {
	var rec = &Reconciler{
		cl:     mgr.GetClient(),
		rdr:    mgr.GetAPIReader(),
		sch:    mgr.GetScheme(),
		log:    slog.Default(),
		logAlt: mgr.GetLogger(),
	}

	type TReq = Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	enqueueRV := func(obj client.Object, q TQueue) {
		rv, ok := obj.(*v1alpha3.ReplicatedVolume)
		if !ok {
			return
		}
		q.Add(RecalculateRequest{VolumeName: rv.Name})
	}

	enqueueRVR := func(obj client.Object, q TQueue) {
		rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica)
		if !ok {
			return
		}
		if rvr.Spec.ReplicatedVolumeName == "" {
			return
		}
		q.Add(RecalculateRequest{VolumeName: rvr.Spec.ReplicatedVolumeName})
	}

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("rvr_tie_breaker_count_controller").
		Watches(
			&v1alpha3.ReplicatedVolume{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					_ context.Context,
					ev event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					enqueueRV(ev.Object, q)
				},
				UpdateFunc: func(
					_ context.Context,
					ev event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					enqueueRV(ev.ObjectNew, q)
				},
				DeleteFunc: func(
					_ context.Context,
					ev event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					enqueueRV(ev.Object, q)
				},
				GenericFunc: func(
					_ context.Context,
					ev event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					enqueueRV(ev.Object, q)
				},
			}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					_ context.Context,
					ev event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					enqueueRVR(ev.Object, q)
				},
				UpdateFunc: func(
					_ context.Context,
					ev event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					enqueueRVR(ev.ObjectNew, q)
				},
				DeleteFunc: func(
					_ context.Context,
					ev event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					enqueueRVR(ev.Object, q)
				},
				GenericFunc: func(
					_ context.Context,
					ev event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					enqueueRVR(ev.Object, q)
				},
			}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}


