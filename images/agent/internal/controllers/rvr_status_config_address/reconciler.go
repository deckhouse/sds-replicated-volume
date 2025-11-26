package rvrstatusconfigaddress

import (
	"context"
	"log/slog"

	e "github.com/deckhouse/sds-replicated-volume/images/agent/internal/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl  client.Client
	rdr client.Reader
	sch *runtime.Scheme
	log *slog.Logger
}

var _ reconcile.TypedReconciler[Request] = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
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
