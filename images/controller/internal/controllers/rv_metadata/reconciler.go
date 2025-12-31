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

package rvmetadata

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
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
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.log.Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting rv: %w", err)
	}

	log := r.log.With("rvName", rv.Name)

	patch := client.MergeFrom(rv.DeepCopy())

	finalizerChanged, err := r.processFinalizers(ctx, log, rv)
	if err != nil {
		return reconcile.Result{}, err
	}

	labelChanged := r.processLabels(log, rv)

	if finalizerChanged || labelChanged {
		if err := r.cl.Patch(ctx, rv, patch); err != nil {
			if client.IgnoreNotFound(err) == nil {
				log.Info("ReplicatedVolume was deleted during reconciliation, skipping patch")
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("patching rv metadata: %w", err)
		}
	}
	return reconcile.Result{}, nil
}

// processLabels ensures required labels are set on the RV.
// Returns true if any label was changed.
func (r *Reconciler) processLabels(log *slog.Logger, rv *v1alpha1.ReplicatedVolume) bool {
	var changed bool

	// Set replicated-storage-class label from spec
	if rv.Spec.ReplicatedStorageClassName != "" {
		rv.Labels, changed = v1alpha1.EnsureLabel(
			rv.Labels,
			v1alpha1.LabelReplicatedStorageClass,
			rv.Spec.ReplicatedStorageClassName,
		)
		if changed {
			log.Info("replicated-storage-class label set on rv",
				"rsc", rv.Spec.ReplicatedStorageClassName)
		}
	}

	return changed
}

func (r *Reconciler) processFinalizers(
	ctx context.Context,
	log *slog.Logger,
	rv *v1alpha1.ReplicatedVolume,
) (hasChanged bool, err error) {
	rvDeleted := rv.DeletionTimestamp != nil
	rvHasFinalizer := slices.Contains(rv.Finalizers, v1alpha1.ControllerAppFinalizer)

	var hasRVRs bool
	if rvDeleted {
		hasRVRs, err = r.rvHasRVRs(ctx, log, rv.Name)
		if err != nil {
			return false, err
		}
	} // it doesn't matter otherwise

	if !rvDeleted {
		if !rvHasFinalizer {
			rv.Finalizers = append(rv.Finalizers, v1alpha1.ControllerAppFinalizer)
			log.Info("finalizer added to rv")
			return true, nil
		}
		return false, nil
	}

	if hasRVRs {
		if !rvHasFinalizer {
			rv.Finalizers = append(rv.Finalizers, v1alpha1.ControllerAppFinalizer)
			log.Info("finalizer added to rv")
			return true, nil
		}
		return false, nil
	}

	if rvHasFinalizer {
		rv.Finalizers = slices.DeleteFunc(
			rv.Finalizers,
			func(f string) bool { return f == v1alpha1.ControllerAppFinalizer },
		)
		log.Info("finalizer deleted from rv")
		return true, nil
	}

	return false, nil
}

func (r *Reconciler) rvHasRVRs(ctx context.Context, log *slog.Logger, rvName string) (bool, error) {
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rvName,
	}); err != nil {
		return false, fmt.Errorf("listing rvrs: %w", err)
	}

	for i := range rvrList.Items {
		log.Debug(
			"found rvr 'rvrName' linked to rv 'rvName', therefore skip removing finalizer from rv",
			"rvrName", rvrList.Items[i].Name,
		)
		return true, nil
	}
	return false, nil
}
