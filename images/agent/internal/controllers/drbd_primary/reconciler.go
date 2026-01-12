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

package drbdprimary

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/drbdapierrors"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scanner"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
	cfg    env.Config
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme, cfg env.Config) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
		cfg:    cfg,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling started")
	start := time.Now()
	defer func() {
		log.Info("Reconcile finished", "duration", time.Since(start).String())
	}()

	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(4).Info("ReplicatedVolumeReplica not found, skipping")
			return reconcile.Result{}, nil
		}
		log.Error(err, "getting ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	if !thisNodeRVRShouldEitherBePromotedOrDemotedOrHasErrors(r.cfg.NodeName(), rvr) {
		log.V(4).Info("ReplicatedVolumeReplica does not pass thisNodeRVRShouldEitherBePromotedOrDemotedOrHasErrors check, skipping")
		return reconcile.Result{}, nil
	}

	wantPrimary, actuallyPrimary, initialized := rvrDesiredAndActualRole(rvr)
	if !initialized {
		log.V(4).Info("ReplicatedVolumeReplica is not initialized, skipping")
		return reconcile.Result{}, nil
	}

	// Role already matches - clear errors and return
	if wantPrimary == actuallyPrimary {
		log.V(4).Info("DRBD role already matches desired state", "wantPrimary", wantPrimary, "actuallyPrimary", actuallyPrimary)
		if err := r.clearErrors(ctx, rvr); err != nil {
			log.Error(err, "clearing errors")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	// Check promotion prerequisites
	if wantPrimary && !r.canPromote(log, rvr) {
		return reconcile.Result{}, nil
	}

	// Execute role change
	isDeleting := rvr.DeletionTimestamp != nil
	cmdErr := r.executeRoleChange(ctx, log, rvr.Spec.ReplicatedVolumeName, wantPrimary, isDeleting)

	// Log result
	if cmdErr != nil {
		log.Error(cmdErr, "role change failed",
			"command", cmdErr.CommandWithArgs(),
			"output", cmdErr.Output(),
			"exitCode", cmdErr.ExitCode())
	} else {
		log.V(4).Info("role change succeeded", "wantPrimary", wantPrimary)
	}

	// During cleanup, skip status patch - just return error for retry
	if isDeleting {
		return reconcile.Result{}, cmdErr
	}

	// Update status with error (or clear it)
	if err := r.updateErrorStatus(ctx, rvr, cmdErr, wantPrimary); err != nil {
		log.Error(err, "updating error status")
		return reconcile.Result{}, err
	}

	// Return cmdErr to trigger retry if command failed
	if cmdErr != nil {
		return reconcile.Result{}, cmdErr
	}

	if s := scanner.DefaultScanner(); s != nil {
		(*s).ResourceShouldBeRefreshed(rvr.Spec.ReplicatedVolumeName)
	}

	return reconcile.Result{}, nil
}

// executeRoleChange executes drbdadm primary/secondary command.
// During deletion, falls back to drbdsetup if drbdadm fails.
func (r *Reconciler) executeRoleChange(ctx context.Context, log logr.Logger, rvName string, wantPrimary bool, isDeleting bool) drbdadm.CommandError {
	if wantPrimary {
		log.Info("Promoting to primary")
		return drbdadm.ExecutePrimary(ctx, rvName)
	}

	log.Info("Demoting to secondary")
	cmdErr := drbdadm.ExecuteSecondary(ctx, rvName)

	// During cleanup, fallback to drbdsetup if drbdadm fails (config file may be removed)
	if cmdErr != nil && isDeleting {
		log.Info("drbdadm secondary failed during cleanup, trying drbdsetup secondary",
			"resource", rvName, "error", cmdErr)
		if err := drbdsetup.ExecuteSecondary(ctx, rvName); err != nil {
			log.Error(err, "drbdsetup secondary also failed")
			// Return original drbdadm error since drbdsetup also failed
			return cmdErr
		}
		log.Info("successfully demoted DRBD resource via drbdsetup", "resource", rvName)
		return nil
	}

	return cmdErr
}

func (r *Reconciler) updateErrorStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	cmdErr drbdadm.CommandError,
	isPrimary bool,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())
	apiErrors := drbdapierrors.EnsureErrorsField(rvr)

	// Reset only this controller's errors, then write if there's an error
	resetDRBDPrimaryErrors(apiErrors)

	if cmdErr != nil {
		var drbdErr drbdapierrors.DRBDAPIError
		if isPrimary {
			drbdErr = primaryCommandError{cmdErr}
		} else {
			drbdErr = secondaryCommandError{cmdErr}
		}
		drbdErr.WriteDRBDError(apiErrors)
	}

	return r.cl.Status().Patch(ctx, rvr, patch)
}

func (r *Reconciler) clearErrors(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	// Check if there are any errors to clear
	if allPrimaryErrorsAreNil(rvr) {
		return nil
	}

	patch := client.MergeFrom(rvr.DeepCopy())
	// Clear only this controller's errors (primary and secondary)
	resetDRBDPrimaryErrors(rvr.Status.DRBD.Errors)
	return r.cl.Status().Patch(ctx, rvr, patch)
}

func rvrDesiredAndActualRole(rvr *v1alpha1.ReplicatedVolumeReplica) (wantPrimary bool, actuallyPrimary bool, initialized bool) {
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil || rvr.Status.DRBD.Config.Primary == nil {
		// not initialized
		return
	}

	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil || rvr.Status.DRBD.Status.Role == "" {
		// not initialized
		return
	}

	wantPrimary = *rvr.Status.DRBD.Config.Primary
	actuallyPrimary = rvr.Status.DRBD.Status.Role == "Primary"
	initialized = true
	return
}

func (r *Reconciler) canPromote(log logr.Logger, rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.DeletionTimestamp != nil {
		log.V(1).Info("can not promote, because deleted")
		return false
	}

	if rvr.Status.DRBD.Actual == nil || !rvr.Status.DRBD.Actual.InitialSyncCompleted {
		log.V(1).Info("can not promote, because initialSyncCompleted is false")
		return false
	}

	return true
}

func allPrimaryErrorsAreNil(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Errors == nil {
		return true
	}
	if rvr.Status.DRBD.Errors.LastPrimaryError == nil && rvr.Status.DRBD.Errors.LastSecondaryError == nil {
		return true
	}
	return false
}
