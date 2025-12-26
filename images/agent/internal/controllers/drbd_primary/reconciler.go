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
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
	cfg    env.Config
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

const (
	reconcileAfter = 10 * time.Second
)

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

	// Check if this RVR belongs to this node
	if rvr.Spec.NodeName != r.cfg.NodeName() {
		log.V(4).Info("ReplicatedVolumeReplica does not belong to this node, skipping")
		return reconcile.Result{}, nil
	}

	if !rvr.DeletionTimestamp.IsZero() && !v1alpha1.HasExternalFinalizers(rvr) {
		log.Info("ReplicatedVolumeReplica is being deleted, ignoring reconcile request")
		return reconcile.Result{}, nil
	}

	ready, reason := r.rvrIsReady(rvr)
	if !ready {
		log.V(4).Info("ReplicatedVolumeReplica is not ready, skipping", "reason", reason)
		return reconcile.Result{}, nil
	}

	// Check if ReplicatedVolume is IOReady
	ready, err = r.rvIsReady(ctx, rvr.Spec.ReplicatedVolumeName)
	if err != nil {
		log.Error(err, "checking ReplicatedVolume")
		return reconcile.Result{}, err
	}
	if !ready {
		log.V(4).Info("ReplicatedVolume is not Ready, requeuing", "rvName", rvr.Spec.ReplicatedVolumeName)
		return reconcile.Result{
			RequeueAfter: reconcileAfter,
		}, nil
	}

	desiredPrimary := *rvr.Status.DRBD.Config.Primary
	currentRole := rvr.Status.DRBD.Status.Role

	// Check if role change is needed
	needPrimary := desiredPrimary && currentRole != "Primary"
	needSecondary := !desiredPrimary && currentRole == "Primary"

	if !needPrimary && !needSecondary {
		log.V(4).Info("DRBD role already matches desired state", "role", currentRole, "desiredPrimary", desiredPrimary)
		// Clear any previous errors
		err = r.clearErrors(ctx, rvr)
		if err != nil {
			log.Error(err, "clearing errors")
		}
		return reconcile.Result{}, err
	}

	// Execute drbdadm command
	var cmdErr error
	var cmdOutput string
	var exitCode int

	if needPrimary {
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
		log.Error(cmdErr, "executed command failed", "command", drbdadm.Command, "args", map[bool][]string{true: drbdadm.PrimaryArgs(rvr.Spec.ReplicatedVolumeName), false: drbdadm.SecondaryArgs(rvr.Spec.ReplicatedVolumeName)}[needPrimary], "output", cmdOutput)
	} else {
		log.V(4).Info("executed command successfully", "command", drbdadm.Command, "args", map[bool][]string{true: drbdadm.PrimaryArgs(rvr.Spec.ReplicatedVolumeName), false: drbdadm.SecondaryArgs(rvr.Spec.ReplicatedVolumeName)}[needPrimary])
	}

	// Update status with error or clear it
	err = r.updateErrorStatus(ctx, rvr, cmdErr, cmdOutput, exitCode, needPrimary)
	if err != nil {
		log.Error(err, "updating error status")
	}
	return reconcile.Result{}, err
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

	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
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
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Errors == nil {
		return nil
	}

	// Only patch if there are errors to clear
	if rvr.Status.DRBD.Errors.LastPrimaryError == nil && rvr.Status.DRBD.Errors.LastSecondaryError == nil {
		return nil
	}

	patch := client.MergeFrom(rvr.DeepCopy())
	// Clear primary and secondary errors since role is already correct
	rvr.Status.DRBD.Errors.LastPrimaryError = nil
	rvr.Status.DRBD.Errors.LastSecondaryError = nil
	return r.cl.Status().Patch(ctx, rvr, patch)
}

// rvrIsReady checks if ReplicatedVolumeReplica is ready for primary/secondary operations.
// It returns true if all required fields are present, false otherwise.
// The second return value contains a reason string when the RVR is not ready.
func (r *Reconciler) rvrIsReady(rvr *v1alpha1.ReplicatedVolumeReplica) (bool, string) {
	// rvr.spec.nodeName will be set once and will not change again.
	if rvr.Spec.NodeName == "" {
		return false, "ReplicatedVolumeReplica does not have a nodeName"
	}

	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil || rvr.Status.DRBD.Actual == nil {
		return false, "DRBD status not initialized"
	}

	// Check if we need to execute drbdadm primary or secondary
	if rvr.Status.DRBD.Config == nil || rvr.Status.DRBD.Config.Primary == nil {
		return false, "DRBD config primary not set"
	}

	if !rvr.Status.DRBD.Actual.InitialSyncCompleted {
		return false, "Initial sync not completed, skipping"
	}

	return true, ""
}

// rvIsReady checks if the ReplicatedVolume is IOReady.
// It returns true if the ReplicatedVolume exists and has IOReady condition set to True,
// false if the condition is not True, and an error if the ReplicatedVolume cannot be retrieved.
func (r *Reconciler) rvIsReady(ctx context.Context, rvName string) (bool, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	err := r.cl.Get(ctx, client.ObjectKey{Name: rvName}, rv)
	if err != nil {
		return false, err
	}

	if !v1alpha1.HasControllerFinalizer(rv) {
		return false, nil
	}

	if rv.Status == nil {
		return false, nil
	}

	return meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVIOReady), nil
}
