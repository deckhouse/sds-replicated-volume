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

package rvattachcontroller

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

var _ reconcile.Reconciler = &Reconciler{}

const (
	ConditionTypeAttachSucceeded           = "AttachSucceeded"
	ReasonUnableToProvideLocalVolumeAccess = "UnableToProvideLocalVolumeAccess"
)

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	// fetch target ReplicatedVolume; if it was deleted, stop reconciliation
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: req.Name}, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to get ReplicatedVolume")
		return reconcile.Result{}, err
	}

	// check basic preconditions from spec before doing any work
	if shouldSkipRV(rv, log) {
		return reconcile.Result{}, nil
	}

	// load ReplicatedStorageClass and all replicas of this RV
	rsc, replicasForRV, err := r.loadAttachContext(ctx, rv, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	// validate local access constraints for volumeAccess=Local; may set AttachSucceeded=False and stop
	finish, err := r.checkIfLocalAccessHasEnoughDiskfulReplicas(ctx, rv, rsc, replicasForRV, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if finish {
		return reconcile.Result{}, nil
	}

	// sync rv.status.drbd.config.allowTwoPrimaries and, when needed, wait until it is actually applied on replicas
	if err := r.syncAllowTwoPrimaries(ctx, rv, log); err != nil {
		return reconcile.Result{}, err
	}

	if ready, err := r.waitForAllowTwoPrimariesApplied(ctx, rv, log); err != nil || !ready {
		return reconcile.Result{}, err
	}

	// sync primary roles on replicas and rv.status.attachedTo
	if err := r.syncReplicaPrimariesAndAttachedTo(ctx, rv, replicasForRV, log); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// loadAttachContext fetches ReplicatedStorageClass and all non-deleted replicas
// for the given ReplicatedVolume. It returns data needed for attach logic.
func (r *Reconciler) loadAttachContext(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
) (*v1alpha1.ReplicatedStorageClass, []v1alpha1.ReplicatedVolumeReplica, error) {
	// read ReplicatedStorageClass to understand volumeAccess and other policies
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "unable to get ReplicatedStorageClass")
		return nil, nil, err
	}

	// list all ReplicatedVolumeReplica objects and filter those that belong to this RV
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return nil, nil, err
	}

	var replicasForRV []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrList.Items {
		// select replicas of this volume that are not marked for deletion
		if rvr.Spec.ReplicatedVolumeName == rv.Name && rvr.DeletionTimestamp.IsZero() {
			replicasForRV = append(replicasForRV, rvr)
		}
	}

	return rsc, replicasForRV, nil
}

// checkIfLocalAccessHasEnoughDiskfulReplicas enforces the rule that for volumeAccess=Local there must be
// a Diskful replica on each node from rv.spec.attachTo. On violation it sets
// AttachSucceeded=False and stops reconciliation.
func (r *Reconciler) checkIfLocalAccessHasEnoughDiskfulReplicas(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRVList []v1alpha1.ReplicatedVolumeReplica,
	log logr.Logger,
) (bool, error) {
	// this validation is relevant only when volumeAccess is Local
	if rsc.Spec.VolumeAccess != "Local" {
		return false, nil
	}

	// map replicas by NodeName for efficient lookup
	NodeNameToRvrMap := make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(replicasForRVList))
	for _, rvr := range replicasForRVList {
		NodeNameToRvrMap[rvr.Spec.NodeName] = &rvr
	}

	// In case rsc.spec.volumeAccess==Local, but replica is not Diskful or doesn't exist,
	// promotion is impossible: update AttachSucceeded on RV and stop reconcile.
	for _, attachNodeName := range rv.Spec.AttachTo {
		rvr, ok := NodeNameToRvrMap[attachNodeName]
		if !ok || rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			patchedRV := rv.DeepCopy()
			if patchedRV.Status == nil {
				patchedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
			}
			meta.SetStatusCondition(&patchedRV.Status.Conditions, metav1.Condition{
				Type:    ConditionTypeAttachSucceeded,
				Status:  metav1.ConditionFalse,
				Reason:  ReasonUnableToProvideLocalVolumeAccess,
				Message: fmt.Sprintf("Local access required but no Diskful replica found on node %s", attachNodeName),
			})

			if err := r.cl.Status().Patch(ctx, patchedRV, client.MergeFrom(rv)); err != nil {
				log.Error(err, "unable to update ReplicatedVolume AttachSucceeded=False")
				return true, err
			}

			// stop reconciliation after setting the failure condition
			return true, nil
		}
	}

	return false, nil
}

// syncAllowTwoPrimaries updates rv.status.drbd.config.allowTwoPrimaries according to
// the number of nodes in rv.spec.attachTo. Waiting for actual application on
// replicas is handled separately by waitForAllowTwoPrimariesApplied.
func (r *Reconciler) syncAllowTwoPrimaries(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
) error {
	desiredAllowTwoPrimaries := len(rv.Spec.AttachTo) == 2

	if rv.Status != nil &&
		rv.Status.DRBD != nil &&
		rv.Status.DRBD.Config != nil &&
		rv.Status.DRBD.Config.AllowTwoPrimaries == desiredAllowTwoPrimaries {
		return nil
	}

	patchedRV := rv.DeepCopy()

	if patchedRV.Status == nil {
		patchedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	if patchedRV.Status.DRBD == nil {
		patchedRV.Status.DRBD = &v1alpha1.DRBDResource{}
	}
	if patchedRV.Status.DRBD.Config == nil {
		patchedRV.Status.DRBD.Config = &v1alpha1.DRBDResourceConfig{}
	}
	patchedRV.Status.DRBD.Config.AllowTwoPrimaries = desiredAllowTwoPrimaries

	if err := r.cl.Status().Patch(ctx, patchedRV, client.MergeFrom(rv)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolume allowTwoPrimaries")
			return err
		}

		// RV was deleted concurrently; nothing left to attach for
		return nil
	}

	return nil
}

func (r *Reconciler) waitForAllowTwoPrimariesApplied(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
) (bool, error) {
	if len(rv.Spec.AttachTo) != 2 {
		return true, nil
	}

	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica while waiting for allowTwoPrimaries")
		return false, err
	}

	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name || !rvr.DeletionTimestamp.IsZero() {
			continue
		}

		// Skip replicas without a node (unscheduled replicas or TieBreaker without node assignment)
		// as they are not configured by the agent and won't have actual.allowTwoPrimaries set
		if rvr.Spec.NodeName == "" {
			continue
		}

		if rvr.Status == nil ||
			rvr.Status.DRBD == nil ||
			rvr.Status.DRBD.Actual == nil ||
			!rvr.Status.DRBD.Actual.AllowTwoPrimaries {
			return false, nil
		}
	}

	return true, nil
}

// syncReplicaPrimariesAndAttachedTo updates rvr.status.drbd.config.primary (and spec.type for TieBreaker)
// for all replicas according to rv.spec.attachTo and recomputes rv.status.attachedTo
// from actual DRBD roles on replicas.
func (r *Reconciler) syncReplicaPrimariesAndAttachedTo(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	replicasForRV []v1alpha1.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	// desired primary set: replicas on nodes from rv.spec.attachTo should be primary
	attachSet := make(map[string]struct{}, len(rv.Spec.AttachTo))
	for _, nodeName := range rv.Spec.AttachTo {
		attachSet[nodeName] = struct{}{}
	}

	var rvrPatchErr error
	for i := range replicasForRV {
		rvr := &replicasForRV[i]

		if rvr.Spec.NodeName == "" {
			if err := r.patchRVRStatusConditions(ctx, log, rvr, false); err != nil {
				rvrPatchErr = errors.Join(rvrPatchErr, err)
			}
			continue
		}

		_, shouldBePrimary := attachSet[rvr.Spec.NodeName]

		if shouldBePrimary && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
			if err := r.patchRVRTypeToAccess(ctx, log, rvr); err != nil {
				rvrPatchErr = errors.Join(rvrPatchErr, err)
				continue
			}
		}

		if err := r.patchRVRPrimary(ctx, log, rvr, shouldBePrimary); err != nil {
			rvrPatchErr = errors.Join(rvrPatchErr, err)
			continue
		}
	}

	// recompute rv.status.attachedTo from actual DRBD roles on replicas
	attachedTo := make([]string, 0, len(replicasForRV))
	for _, rvr := range replicasForRV {
		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
			continue
		}
		if rvr.Status.DRBD.Status.Role != "Primary" {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		attachedTo = append(attachedTo, rvr.Spec.NodeName)
	}

	patchedRV := rv.DeepCopy()
	if patchedRV.Status == nil {
		patchedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	patchedRV.Status.AttachedTo = attachedTo

	if err := r.cl.Status().Patch(ctx, patchedRV, client.MergeFrom(rv)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolume attachedTo")
			return errors.Join(rvrPatchErr, err)
		}
		// RV was deleted concurrently; nothing left to attach for
	}

	if rvrPatchErr != nil {
		return fmt.Errorf("errors during patching replicas for RV: %w", rvrPatchErr)
	}

	return nil
}

func (r *Reconciler) patchRVRTypeToAccess(
	ctx context.Context,
	log logr.Logger,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) error {
	originalRVR := rvr.DeepCopy()

	rvr.Spec.Type = v1alpha1.ReplicaTypeAccess
	if err := r.cl.Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolumeReplica type to Access")
			return err
		}
	}
	return nil
}

func (r *Reconciler) patchRVRPrimary(
	ctx context.Context,
	log logr.Logger,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	shouldBePrimary bool,
) error {
	originalRVR := rvr.DeepCopy()

	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}
	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
	}

	currentPrimaryValue := false
	if rvr.Status.DRBD.Config.Primary != nil {
		currentPrimaryValue = *rvr.Status.DRBD.Config.Primary
	}
	if currentPrimaryValue != shouldBePrimary {
		rvr.Status.DRBD.Config.Primary = &shouldBePrimary
	}

	_ = rvr.UpdateStatusConditionAttached(shouldBePrimary)

	if err := r.cl.Status().Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolumeReplica primary", "rvr", rvr.Name)
			return err
		}
	}
	return nil
}

func (r *Reconciler) patchRVRStatusConditions(
	ctx context.Context,
	log logr.Logger,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	shouldBePrimary bool,
) error {
	originalRVR := rvr.DeepCopy()

	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}

	_ = rvr.UpdateStatusConditionAttached(shouldBePrimary)

	if err := r.cl.Status().Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to patch ReplicatedVolumeReplica status conditions", "rvr", rvr.Name)
			return err
		}
	}
	return nil
}

// shouldSkipRV returns true when, according to spec, rv-attach-controller
// should not perform any actions for the given ReplicatedVolume.
func shouldSkipRV(rv *v1alpha1.ReplicatedVolume, log logr.Logger) bool {
	if !v1alpha1.HasControllerFinalizer(rv) {
		return true
	}

	// controller works only when status is initialized
	if rv.Status == nil {
		return true
	}

	// controller works only when RV is IOReady according to spec
	if !meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVIOReady) {
		return true
	}

	// fetch ReplicatedStorageClass to inspect volumeAccess mode and other policies
	if rv.Spec.ReplicatedStorageClassName == "" {
		log.Info("ReplicatedStorageClassName is empty, skipping")
		return true
	}

	return false
}
