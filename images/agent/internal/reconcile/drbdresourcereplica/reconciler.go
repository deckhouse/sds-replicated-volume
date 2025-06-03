package drbdresourcereplica

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
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
	req r.TypedRequest[*v1alpha2.DRBDResourceReplica],
) (reconcile.Result, error) {

	r = r.withRequestLogging(req.RequestId(), req.Object())

	var err error
	if req.IsCreate() {
		err = r.onCreate(req.Object())
	} else if req.IsUpdate() {
		err = r.onUpdate(req.Object())
	} else {
		err = r.onDelete()
	}

	return reconcile.Result{}, err
}

func (r *Reconciler) onCreate(repl *v1alpha2.DRBDResourceReplica) error {
	// create res file, if not exist
	// parse res file
	// update resource
	//
	// drbdadm adjust, if needed
	// drbdadm up, if needed
	return nil
}

func (r *Reconciler) onUpdate(repl *v1alpha2.DRBDResourceReplica) error {
	return nil
}

func (r *Reconciler) onDelete() error {
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
