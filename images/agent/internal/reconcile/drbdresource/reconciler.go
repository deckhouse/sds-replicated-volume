package drbdresource

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	r "github.com/deckhouse/sds-replicated-volume/images/agent/internal/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	log *slog.Logger
}

func NewReconciler(log *slog.Logger) *Reconciler {
	return &Reconciler{
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req r.TypedRequest[*v1alpha1.DRBDResource],
) (reconcile.Result, error) {

	r = r.withRequestLogging(req.RequestId(), req.Object())

	var err error
	if req.IsCreate() {
		err = r.CreateDRBDResourceIfNeeded()
	} else if req.IsUpdate() {
		err = r.UpdateDRBDResourceIfNeeded()
	} else {
		err = r.DeleteDRBDResourceIfNeeded()
	}

	return reconcile.Result{}, err
}

func (r *Reconciler) CreateDRBDResourceIfNeeded() error {

	return nil
}

func (r *Reconciler) UpdateDRBDResourceIfNeeded() error {
	return nil
}

func (r *Reconciler) DeleteDRBDResourceIfNeeded() error {
	return nil
}

func (r *Reconciler) withRequestLogging(requestId string, obj client.Object) *Reconciler {
	newRec := *r
	newRec.log = newRec.log.
		With("requestId", requestId).
		With(
			slog.Group("object",
				"namespace", obj.GetNamespace(),
				"name", obj.GetName(),
				"resourceVersion", obj.GetResourceVersion(),
			),
		)
	return &newRec
}
