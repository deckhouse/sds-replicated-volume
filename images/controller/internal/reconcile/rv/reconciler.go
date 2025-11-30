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

package rv

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha2 "github.com/deckhouse/sds-replicated-volume/api/v1alpha2old"
)

type Reconciler struct {
	log *slog.Logger
	cl  client.Client
	rdr client.Reader
	sch *runtime.Scheme
}

func NewReconciler(log *slog.Logger, cl client.Client, rdr client.Reader, sch *runtime.Scheme) *Reconciler {
	return &Reconciler{
		log: log,
		cl:  cl,
		rdr: rdr,
		sch: sch,
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

		if typedReq.PropagatedFromOwnedRVR {
			r.log.Info("PropagatedFromOwnedRVR")
		}

		if typedReq.PropagatedFromOwnedLLV {
			r.log.Info("PropagatedFromOwnedLLV")
		}

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
			ctx:    ctx,
			log:    r.log.WithGroup(reqTypeName).With("name", typedReq.Name),
			cl:     r.cl,
			rdr:    r.rdr,
			scheme: r.sch,
			cfg:    clusterCfg,
			rv:     rvr,
		}

		return reconcile.Result{}, h.Handle()

	case ResourceDeleteRequest:
		rv := &v1alpha2.ReplicatedVolume{}
		err := r.cl.Get(ctx, client.ObjectKey{Name: typedReq.Name}, rv)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				r.log.Warn(
					"rv 'name' not found for delete reconcile, it might be deleted, ignore",
					"name", typedReq.Name,
				)
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("getting rv %s for delete reconcile: %w", typedReq.Name, err)
		}

		h := &resourceDeleteRequestHandler{
			ctx: ctx,
			log: r.log.WithGroup(reqTypeName).With("name", typedReq.Name),
			cl:  r.cl,
			rv:  rv,
		}
		return reconcile.Result{}, h.Handle()

	default:
		r.log.Error("unknown req type", "type", reqTypeName)
		return reconcile.Result{}, nil
	}
}
