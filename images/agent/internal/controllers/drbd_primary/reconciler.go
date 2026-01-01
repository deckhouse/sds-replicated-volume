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
	"errors"
	"os/exec"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scanner"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
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
	err := r.cl.Get(ctx, req.NamespacedName, rvr)
	if err != nil {
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

	if wantPrimary == actuallyPrimary {
		log.V(4).Info("DRBD role already matches desired state", "wantPrimary", wantPrimary, "actuallyPrimary", actuallyPrimary)
		// Clear any previous errors
		err = r.clearErrors(ctx, rvr)
		if err != nil {
			log.Error(err, "clearing errors")
		}
		return reconcile.Result{}, err
	}

	if wantPrimary {
		// promote
		if !r.canPromote(log, rvr) {
			return reconcile.Result{}, nil
		}
	} // we can always demote

	// Execute drbdadm command
	var cmdErr error
	var cmdOutput string
	var exitCode int

	if wantPrimary {
		log.Info("Promoting to primary")
		cmdErr = drbdadm.ExecutePrimary(ctx, rvr.Spec.ReplicatedVolumeName)
	} else {
		log.Info("Demoting to secondary")
		cmdErr = drbdadm.ExecuteSecondary(ctx, rvr.Spec.ReplicatedVolumeName)
	}

	// Extract error details
	if cmdErr != nil {
		var exitErr *exec.ExitError
		if errors.As(cmdErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		// The error from drbdadm.ExecutePrimary/ExecuteSecondary is a joined error
		// containing both the exec error and the command output
		cmdOutput = cmdErr.Error()
		log.Error(cmdErr, "executed command failed",
			"command", drbdadm.Command,
			"args", map[bool][]string{
				true:  drbdadm.PrimaryArgs(rvr.Spec.ReplicatedVolumeName),
				false: drbdadm.SecondaryArgs(rvr.Spec.ReplicatedVolumeName),
			}[wantPrimary],
			"output", cmdOutput)
	} else {
		log.V(4).Info("executed command successfully",
			"command", drbdadm.Command,
			"args", map[bool][]string{
				true:  drbdadm.PrimaryArgs(rvr.Spec.ReplicatedVolumeName),
				false: drbdadm.SecondaryArgs(rvr.Spec.ReplicatedVolumeName),
			}[wantPrimary],
		)
	}

	// Update status with error or clear it
	err = r.updateErrorStatus(ctx, rvr, cmdErr, cmdOutput, exitCode, wantPrimary)
	if err != nil {
		log.Error(err, "updating error status")
		return reconcile.Result{}, err
	}

	s := scanner.DefaultScanner()
	if s != nil {
		(*s).ResourceShouldBeRefreshed(rvr.Spec.ReplicatedVolumeName)
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) updateErrorStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	cmdErr error,
	cmdOutput string,
	exitCode int,
	isPrimary bool,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}
	if rvr.Status.DRBD.Errors == nil {
		rvr.Status.DRBD.Errors = &v1alpha1.DRBDErrors{}
	}

	// Set or clear error based on command result
	if cmdErr != nil {
		// Limit output to 1024 characters as per API validation
		output := cmdOutput
		if len(output) > 1024 {
			output = output[:1024]
		}

		errorField := &v1alpha1.CmdError{
			Output:   output,
			ExitCode: exitCode,
		}

		if isPrimary {
			rvr.Status.DRBD.Errors.LastPrimaryError = errorField
			// Clear secondary error if it exists
			rvr.Status.DRBD.Errors.LastSecondaryError = nil
		} else {
			rvr.Status.DRBD.Errors.LastSecondaryError = errorField
			// Clear primary error if it exists
			rvr.Status.DRBD.Errors.LastPrimaryError = nil
		}
	} else {
		// Clear error on success
		if isPrimary {
			rvr.Status.DRBD.Errors.LastPrimaryError = nil
		} else {
			rvr.Status.DRBD.Errors.LastSecondaryError = nil
		}
	}

	return r.cl.Status().Patch(ctx, rvr, patch)
}

func (r *Reconciler) clearErrors(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	// Check if there are any errors to clear
	if allErrorsAreNil(rvr) {
		return nil
	}

	patch := client.MergeFrom(rvr.DeepCopy())
	// Clear primary and secondary errors since role is already correct
	rvr.Status.DRBD.Errors.LastPrimaryError = nil
	rvr.Status.DRBD.Errors.LastSecondaryError = nil
	return r.cl.Status().Patch(ctx, rvr, patch)
}

func rvrDesiredAndActualRole(rvr *v1alpha1.ReplicatedVolumeReplica) (wantPrimary bool, actuallyPrimary bool, initialized bool) {
	if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil || rvr.Status.DRBD.Config.Primary == nil {
		// not initialized
		return
	}

	if rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil || rvr.Status.DRBD.Status.Role == "" {
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

func allErrorsAreNil(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Status.DRBD == nil || rvr.Status.DRBD.Errors == nil {
		return true
	}
	if rvr.Status.DRBD.Errors.LastPrimaryError == nil && rvr.Status.DRBD.Errors.LastSecondaryError == nil {
		return true
	}
	return false
}
