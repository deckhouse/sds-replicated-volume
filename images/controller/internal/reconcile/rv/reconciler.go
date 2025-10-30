package rv

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	log *slog.Logger
	cl  client.Client
	rdr client.Reader
}

func NewReconciler(log *slog.Logger, cl client.Client, rdr client.Reader) *Reconciler {
	return &Reconciler{
		log: log,
		cl:  cl,
		rdr: rdr,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	reqTypeName := reflect.TypeOf(req).String()
	r.log.Debug("reconciling", "type", reqTypeName)

	clusterCfg, err := GetClusterConfig(ctx, r.cl)
	_ = clusterCfg
	if err != nil {
		return reconcile.Result{}, err
	}

	switch typedReq := req.(type) {
	case ResourceReconcileRequest:
		rvr := &v1alpha2.ReplicatedVolume{}
		err := r.cl.Get(ctx, client.ObjectKey{Name: typedReq.Name}, rvr)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				r.log.Warn(
					"rv 'name' not found, it might be deleted, ignore",
					"name", typedReq.Name,
				)
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("getting rv %s: %w", typedReq.Name, err)
		}

		h := &resourceReconcileRequestHandler{
			ctx: ctx,
			log: r.log.WithGroup(reqTypeName).With("name", typedReq.Name),
			cl:  r.cl,
			rdr: r.rdr,
			cfg: clusterCfg,
			rv:  rvr,
		}

		return reconcile.Result{}, h.Handle()

	case ResourceDeleteRequest:
		// h := &resourceDeleteRequestHandler{
		// 	ctx:                  ctx,
		// 	log:                  r.log.WithGroup(reqTypeName).With("name", typedReq.Name),
		// 	cl:                   r.cl,
		// 	nodeName:             r.nodeName,
		// 	replicatedVolumeName: typedReq.ReplicatedVolumeName,
		// }

		// return reconcile.Result{}, h.Handle()
		return reconcile.Result{}, nil

	default:
		r.log.Error("unknown req type", "type", reqTypeName)
		return reconcile.Result{}, nil
	}
}
