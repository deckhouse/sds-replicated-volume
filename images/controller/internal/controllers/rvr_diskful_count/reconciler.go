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

	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl     client.Client
	rdr    client.Reader
	sch    *runtime.Scheme
	log    *slog.Logger // TODO issues/333 choose one logger of (both work via slogh)
	logAlt logr.Logger
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	// TODO issues/333 reconcile requests here
	switch typedReq := req.(type) {
	case AddFirstRequest:
		return reconcile.Result{}, e.ErrNotImplemented

	case AddSubsequentRequest:
		return reconcile.Result{}, e.ErrNotImplemented
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}
}
