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
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl          client.Client
	log         logr.Logger
	cacheSource DeviceMinorCacheSource
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
func NewReconciler(
	cl client.Client,
	log logr.Logger,
	cacheSource DeviceMinorCacheSource,
) *Reconciler {
	return &Reconciler{
		cl:          cl,
		log:         log,
		cacheSource: cacheSource,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithValues("req", req)
	log.Info("Reconciling")

	// Wait for cache to be ready (blocks until initialized after leader election)
	dmCache, err := r.cacheSource.DeviceMinorCache(ctx)
	if err != nil {
		log.Error(err, "Failed to get device minor cache")
		return reconcile.Result{}, err
	}

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

	dm, err := dmCache.GetOrCreate(rv.Name)
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

func patchDupErr(ctx context.Context, cl client.Client, rv *v1alpha1.ReplicatedVolume, conflictingRVNames []string) error {
	return patchRV(ctx, cl, rv, fmt.Sprintf("duplicate device minor, used in RVs: %s", conflictingRVNames), nil)
}

func patchRV(ctx context.Context, cl client.Client, rv *v1alpha1.ReplicatedVolume, msg string, dm *DeviceMinor) error {
	orig := client.MergeFrom(rv.DeepCopy())

	changeRVErr(rv, msg)
	if dm != nil {
		changeRVDM(rv, *dm)
	}

	if err := cl.Status().Patch(ctx, rv, orig); err != nil {
		return fmt.Errorf("patching rv.status.errors.deviceMinor: %w", err)
	}

	return nil
}

func changeRVErr(rv *v1alpha1.ReplicatedVolume, msg string) {
	if msg == "" {
		if rv.Status == nil || rv.Status.Errors == nil || rv.Status.Errors.DeviceMinor == nil {
			return
		}
		rv.Status.Errors.DeviceMinor = nil
	} else {
		if rv.Status == nil {
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
		}
		if rv.Status.Errors == nil {
			rv.Status.Errors = &v1alpha1.ReplicatedVolumeStatusErrors{}
		}
		rv.Status.Errors.DeviceMinor = &v1alpha1.MessageError{Message: msg}
	}
}

func changeRVDM(rv *v1alpha1.ReplicatedVolume, dm DeviceMinor) {
	if rv.Status == nil {
		rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	if rv.Status.DRBD == nil {
		rv.Status.DRBD = &v1alpha1.DRBDResource{}
	}
	if rv.Status.DRBD.Config == nil {
		rv.Status.DRBD.Config = &v1alpha1.DRBDResourceConfig{}
	}
	rv.Status.DRBD.Config.DeviceMinor = u.Ptr(uint(dm))
}
