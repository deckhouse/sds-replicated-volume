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

package rvrstatusconfigaddress

import (
	"context"
	"log/slog"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	e "github.com/deckhouse/sds-replicated-volume/images/agent/internal/errors"
)

type Reconciler struct {
	cl  client.Client
	rdr client.Reader
	sch *runtime.Scheme
	log *slog.Logger
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

func (r *Reconciler) Reconcile(
	_ context.Context,
	req Request,
) (reconcile.Result, error) {
	switch typedReq := req.(type) {
	case MainRequest:
		return reconcile.Result{}, e.ErrNotImplemented

	case AlternativeRequest:
		return reconcile.Result{}, e.ErrNotImplemented
	default:
		r.log.Error("unknown req type", "typedReq", typedReq)
		return reconcile.Result{}, e.ErrNotImplemented
	}
}
