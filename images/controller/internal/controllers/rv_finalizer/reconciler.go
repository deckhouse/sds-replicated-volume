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

package rvfinalizer

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
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

	patch := client.MergeFrom(rv.DeepCopy())

	if rvNeedsFinalizer(rv) {
		rv.Finalizers = append(rv.Finalizers, v1alpha3.ControllerAppFinalizer)

		log.Info("finalizer added to rv")
	} else if rvFinalizerMayNeedToBeRemoved(rv) {
		rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
		if err := r.cl.List(ctx, rvrList); err != nil {
			return reconcile.Result{}, fmt.Errorf("listing rvrs: %w", err)
		}

		for i := range rvrList.Items {
			if rvrList.Items[i].Spec.ReplicatedVolumeName == rv.Name {
				log.Debug(
					"found rvr 'rvrName' linked to rv 'rvName', therefore skip removing finalizer from rv",
					"rvrName", rvrList.Items[i].Name,
				)
				return reconcile.Result{}, nil
			}
		}

		rv.Finalizers = slices.DeleteFunc(
			rv.Finalizers,
			func(f string) bool { return f == v1alpha3.ControllerAppFinalizer },
		)

		log.Info("finalizer deleted from rv")
	}

	if err := r.cl.Patch(ctx, rv, patch); err != nil {
		return reconcile.Result{}, fmt.Errorf("patching rv finalizers: %w", err)
	}
	return reconcile.Result{}, nil
}

func rvNeedsFinalizer(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp == nil && !slices.Contains(rv.Finalizers, v1alpha3.ControllerAppFinalizer)
}
func rvFinalizerMayNeedToBeRemoved(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp != nil && slices.Contains(rv.Finalizers, v1alpha3.ControllerAppFinalizer)
}
