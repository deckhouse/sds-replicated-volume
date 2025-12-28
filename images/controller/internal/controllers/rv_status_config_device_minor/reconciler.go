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

package rvstatusconfigdeviceminor

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl      client.Client
	log     logr.Logger
	dmCache *DeviceMinorCache
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
func NewReconciler(
	cl client.Client,
	log logr.Logger,
	dmCache *DeviceMinorCache,
) *Reconciler {
	return &Reconciler{
		cl:      cl,
		log:     log,
		dmCache: dmCache,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithValues("req", req)
	log.Info("Reconciling")

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, err
	}

	// TODO: is this needed? If yes, also update dm cache initialization and predicates
	// if !v1alpha1.HasControllerFinalizer(rv) {
	// 	log.Info("ReplicatedVolume does not have controller finalizer, skipping")
	// 	return reconcile.Result{}, nil
	// }

	dm, err := r.dmCache.GetOrCreate(rv.Name)
	if err != nil {
		if patchErr := patchRV(ctx, r.cl, rv, err.Error(), nil); patchErr != nil {
			err = errors.Join(err, patchErr)
		}
		return reconcile.Result{}, err
	}

	if err := patchRV(ctx, r.cl, rv, "", &dm); err != nil {
		return reconcile.Result{}, err

	}

	log.Info("assigned deviceMinor to RV", "deviceMinor", dm)

	return reconcile.Result{}, nil
}
