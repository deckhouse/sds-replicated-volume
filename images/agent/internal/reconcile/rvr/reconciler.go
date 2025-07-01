package rvr

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var resourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

type Reconciler struct {
	log      *slog.Logger
	cl       client.Client
	nodeName string
}

func NewReconciler(log *slog.Logger, cl client.Client, nodeName string) *Reconciler {
	return &Reconciler{
		log:      log,
		cl:       cl,
		nodeName: nodeName,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req Request,
) (reconcile.Result, error) {
	reqTypeName := reflect.TypeOf(req).String()
	r.log.Debug("reconciling", "type", reqTypeName)

	clusterCfg, err := GetClusterConfig(ctx, r.cl)
	if err != nil {
		return reconcile.Result{}, err
	}

	switch typedReq := req.(type) {
	case ResourceReconcileRequest:
		rvr := &v1alpha2.ReplicatedVolumeReplica{}
		err := r.cl.Get(ctx, client.ObjectKey{Name: typedReq.Name}, rvr)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("getting rvr %s: %w", typedReq.Name, err)
		}

		if rvr.Spec.NodeName != r.nodeName {
			return reconcile.Result{},
				fmt.Errorf("expected spec.nodeName to be %s, got %s",
					r.nodeName, rvr.Spec.NodeName,
				)
		}

		h := &resourceReconcileRequestHandler{
			ctx:      ctx,
			log:      r.log.WithGroup(reqTypeName).With("name", typedReq.Name),
			cl:       r.cl,
			nodeName: r.nodeName,
			cfg:      clusterCfg,
			rvr:      rvr,
		}

		return reconcile.Result{}, h.Handle()
	default:
		r.log.Error("unknown req type", "type", reqTypeName)
		return reconcile.Result{}, nil
	}
}
