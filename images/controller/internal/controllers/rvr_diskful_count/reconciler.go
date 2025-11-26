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
