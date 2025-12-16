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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl  client.Client
	log *slog.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

func NewReconciler(cl client.Client, log *slog.Logger) *Reconciler {
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting rv: %w", err)
	}

	log := r.log.With("rvName", rv.Name)

	if !linkedRVRsNeedToBeDeleted(rv) {
		log.Debug("linked do not need to be deleted")
		return reconcile.Result{}, nil
	}

	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing rvrs: %w", err)
	}

	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		if rvr.Spec.ReplicatedVolumeName == rv.Name && rvr.DeletionTimestamp == nil {
			if err := r.cl.Delete(ctx, rvr); err != nil {
				return reconcile.Result{}, fmt.Errorf("deleting rvr: %w", err)
			}

			log.Info("deleted rvr", "rvrName", rvr.Name)
		}
	}

	log.Info("finished rvr deletion")
	return reconcile.Result{}, nil
}

func linkedRVRsNeedToBeDeleted(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp == nil
}
