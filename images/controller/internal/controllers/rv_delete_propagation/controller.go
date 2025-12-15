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

package rvdeletepropagation

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

type TReq = Request
type TQueue = workqueue.TypedRateLimitingInterface[TReq]

func BuildController(mgr manager.Manager) error {
	var rec = &Reconciler{
		cl:  mgr.GetClient(),
		log: slog.Default(),
	}

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named(ControllerName).
		Watches(
			&v1alpha3.ReplicatedVolume{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					e event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					rv := e.Object.(*v1alpha3.ReplicatedVolume)
					rec.OnRVCreateOrUpdate(ctx, rv, q)
				},
				UpdateFunc: func(
					ctx context.Context,
					e event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					rv := e.ObjectNew.(*v1alpha3.ReplicatedVolume)
					rec.OnRVCreateOrUpdate(ctx, rv, q)
				},
			}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
