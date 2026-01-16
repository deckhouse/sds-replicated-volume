/*
Copyright 2026 Flant JSC

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

package rvrmetadata

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !rvr.DeletionTimestamp.IsZero() && !obju.HasFinalizersOtherThan(rvr, v1alpha1.ControllerFinalizer, v1alpha1.AgentFinalizer) {
		return reconcile.Result{}, nil
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		return reconcile.Result{}, nil
	}

	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	originalRVR := rvr.DeepCopy()

	// Set owner reference
	if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
		log.Error(err, "unable to set controller reference")
		return reconcile.Result{}, err
	}

	// Process labels
	labelsChanged := r.processLabels(log, rvr, rv)

	ownerRefChanged := !ownerReferencesUnchanged(originalRVR, rvr)

	if !ownerRefChanged && !labelsChanged {
		return reconcile.Result{}, nil
	}

	if err := r.cl.Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedVolumeReplica was deleted during reconciliation, skipping patch", "rvr", rvr.Name)
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to patch ReplicatedVolumeReplica metadata", "rvr", rvr.Name)
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// processLabels ensures required labels are set on the RVR.
// Returns true if any label was changed.
func (r *Reconciler) processLabels(log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) bool {
	var changed, labelChanged bool

	// Set replicated-volume label from spec
	if rvr.Spec.ReplicatedVolumeName != "" {
		labelChanged = obju.SetLabel(rvr, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName)
		if labelChanged {
			log.V(1).Info("replicated-volume label set on rvr",
				"rv", rvr.Spec.ReplicatedVolumeName)
			changed = true
		}
	}

	// Set replicated-storage-class label from RV
	if rv.Spec.ReplicatedStorageClassName != "" {
		labelChanged = obju.SetLabel(rvr, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName)
		if labelChanged {
			log.V(1).Info("replicated-storage-class label set on rvr",
				"rsc", rv.Spec.ReplicatedStorageClassName)
			changed = true
		}
	}

	// Note: node-name label (sds-replicated-volume.deckhouse.io/node-name) is managed
	// by rvr_scheduling_controller, which sets it when scheduling and restores if manually removed.

	return changed
}

func ownerReferencesUnchanged(before, after *v1alpha1.ReplicatedVolumeReplica) bool {
	return reflect.DeepEqual(before.OwnerReferences, after.OwnerReferences)
}
