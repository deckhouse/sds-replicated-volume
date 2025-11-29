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

package rvrdiskfulcount

import (
	"context"
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func BuildController(mgr manager.Manager) error {

	// TODO issues/333 your global dependencies
	var rec = &Reconciler{
		cl:     mgr.GetClient(),
		rdr:    mgr.GetAPIReader(),
		sch:    mgr.GetScheme(),
		log:    slog.Default(),
		logAlt: mgr.GetLogger(),
	}

	type TReq = Request
	type TQueue = workqueue.TypedRateLimitingInterface[TReq]

	err := builder.TypedControllerManagedBy[TReq](mgr).
		Named("rvr_diskful_count_controller").
		Watches(
			&v1alpha3.ReplicatedVolume{},
			&handler.TypedFuncs[client.Object, TReq]{
				CreateFunc: func(
					ctx context.Context,
					ce event.TypedCreateEvent[client.Object],
					q TQueue,
				) {
					// TODO issues/333 filter events here
				},
				UpdateFunc: func(
					ctx context.Context,
					ue event.TypedUpdateEvent[client.Object],
					q TQueue,
				) {
					// TODO issues/333 filter events here
				},
				DeleteFunc: func(
					ctx context.Context,
					de event.TypedDeleteEvent[client.Object],
					q TQueue,
				) {
					// TODO issues/333 filter events here
				},
				GenericFunc: func(
					ctx context.Context,
					ge event.TypedGenericEvent[client.Object],
					q TQueue,
				) {
					// TODO issues/333 filter events here
				},
			}).
		Complete(rec)

	if err != nil {
		// TODO issues/333 log errors early
		// TODO issues/333 use typed errors
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
