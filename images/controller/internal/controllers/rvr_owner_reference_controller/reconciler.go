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

package rvrownerreferencecontroller

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)

	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !rvr.DeletionTimestamp.IsZero() && !v1alpha3.HasExternalFinalizers(rvr) {
		return reconcile.Result{}, nil
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		return reconcile.Result{}, nil
	}

	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	originalRVR := rvr.DeepCopy()

	if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
		log.Error(err, "unable to set controller reference")
		return reconcile.Result{}, err
	}

	if ownerReferencesUnchanged(originalRVR, rvr) {
		return reconcile.Result{}, nil
	}

	if err := r.cl.Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		log.Error(err, "unable to patch ReplicatedVolumeReplica ownerReference", "rvr", rvr.Name)
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func ownerReferencesUnchanged(before, after *v1alpha3.ReplicatedVolumeReplica) bool {
	return reflect.DeepEqual(before.OwnerReferences, after.OwnerReferences)
}
