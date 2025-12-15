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
	"fmt"
	"log/slog"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl  client.Client
	log *slog.Logger
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

func NewReconciler(cl client.Client, rdr client.Reader, log *slog.Logger, nodeName string) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) OnRVCreateOrUpdate(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	q TQueue,
) {
	if linkedRVRsNeedToBeDeleted(rv) {
		q.Add(DeleteLinkedRVRsRequest{RVName: rv.Name})
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, types.NamespacedName{Name: req.GetRVName()}, rv); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting rv: %w", err)
	}

	log := r.log.With("rvName", rv.Name)

	var handle func(ctx context.Context) error
	switch typedReq := req.(type) {
	case DeleteLinkedRVRsRequest:
		handle = (&DeleteLinkedRVRsHandler{
			cl:     r.cl,
			log:    log.With("handler", "AddFinalizerHandler"),
			rvName: rv.Name,
		}).Handle
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}

	return reconcile.Result{}, u.LogError(log, handle(ctx))
}
