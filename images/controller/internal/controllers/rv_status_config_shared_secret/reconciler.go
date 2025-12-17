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

package rvstatusconfigsharedsecret

import (
	"context"
	"slices"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
// This is primarily used for testing, as fields are private.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Get the RV
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !v1alpha3.HasControllerFinalizer(rv.ObjectMeta) {
		log.Info("ReplicatedVolume does not have controller finalizer, skipping")
		return reconcile.Result{}, nil
	}

	// Check if sharedSecret is not set - generate new one
	if rv.Status == nil || rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil || rv.Status.DRBD.Config.SharedSecret == "" {
		return r.reconcileGenerateSharedSecret(ctx, rv, log)
	}

	// Check for UnsupportedAlgorithm errors in RVRs and switch algorithm if needed, also generates new SharedSecret, if needed.
	return r.reconcileSwitchAlgorithm(ctx, rv, log)
}

// reconcileGenerateSharedSecret generates a new shared secret and selects the first algorithm
func (r *Reconciler) reconcileGenerateSharedSecret(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Check if sharedSecret is already set (idempotent check on original)
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil && rv.Status.DRBD.Config.SharedSecret != "" {
		log.V(1).Info("sharedSecret already set and valid", "algorithm", rv.Status.DRBD.Config.SharedSecretAlg)
		return reconcile.Result{}, nil // Already set, nothing to do (idempotent)
	}

	// Update RV status with shared secret
	// If there's a conflict (409), return error - next reconciliation will solve it
	// Race condition handling: If two reconciles run simultaneously, one will get 409 Conflict on Patch.
	// The next reconciliation will check if sharedSecret is already set and skip generation.
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Generate new shared secret using UUID v4 (36 characters, fits DRBD limit of 64)
	// UUID provides uniqueness and randomness required for peer authentication
	sharedSecret := uuid.New().String()
	algorithm := v1alpha3.SharedSecretAlgorithms()[0] // Start with first algorithm (sha256)

	log.Info("Generating new shared secret", "algorithm", algorithm)

	// Initialize status if needed
	ensureRVStatusInitialized(changedRV)

	// Set shared secret and algorithm
	changedRV.Status.DRBD.Config.SharedSecret = sharedSecret
	changedRV.Status.DRBD.Config.SharedSecretAlg = algorithm

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with shared secret")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Generated shared secret")
	return reconcile.Result{}, nil
}

// buildAlgorithmLogFields builds structured logging fields for algorithm-related logs
// logFields: structured logging fields for debugging algorithm operations
func buildAlgorithmLogFields(
	rv *v1alpha3.ReplicatedVolume,
	currentAlg string,
	nextAlgorithm string,
	maxFailedIndex int,
	maxFailedRVR *v1alpha3.ReplicatedVolumeReplica,
	algorithms []string,
	failedNodeNames []string,
) []any {
	logFields := []any{
		"rv", rv.Name,
		"from", currentAlg,
		"to", nextAlgorithm,
	}

	if maxFailedRVR != nil {
		logFields = append(logFields,
			"maxFailedIndex", maxFailedIndex,
			"maxFailedRVR", maxFailedRVR.Name,
			"maxFailedRVRNode", maxFailedRVR.Spec.NodeName,
			"maxFailedAlgorithm", algorithms[maxFailedIndex],
		)
	} else {
		logFields = append(logFields, "maxFailedIndex", maxFailedIndex)
	}

	if len(failedNodeNames) > 0 {
		logFields = append(logFields, "failedNodes", failedNodeNames)
	}

	return logFields
}

// reconcileSwitchAlgorithm checks RVRs for UnsupportedAlgorithm errors and switches to next algorithm
func (r *Reconciler) reconcileSwitchAlgorithm(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Get all RVRs
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Collect all RVRs for this RV with errors
	var rvrsWithErrors []*v1alpha3.ReplicatedVolumeReplica
	var failedNodeNames []string
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name {
			continue
		}
		if hasUnsupportedAlgorithmError(&rvr) {
			failedNodeNames = append(failedNodeNames, rvr.Spec.NodeName)
			rvrsWithErrors = append(rvrsWithErrors, &rvr)
		}
	}

	// If no errors found, nothing to do
	if len(failedNodeNames) == 0 {
		return reconcile.Result{}, nil
	}

	algorithms := v1alpha3.SharedSecretAlgorithms()

	// Find maximum index among all failed algorithms and RVR with max algorithm
	maxFailedIndex := -1
	var maxFailedRVR *v1alpha3.ReplicatedVolumeReplica
	var rvrsWithoutAlg []string
	// rvrsWithUnknownAlg: RVRs with unknown algorithms (not in SharedSecretAlgorithms list)
	// This is unlikely but possible if the algorithm list changes (e.g., algorithm removed or renamed)
	var rvrsWithUnknownAlg []string
	for _, rvr := range rvrsWithErrors {
		// Access UnsupportedAlg directly, checking for nil
		var unsupportedAlg string
		if rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Errors != nil &&
			rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil {
			unsupportedAlg = rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg
		}

		if unsupportedAlg == "" {
			rvrsWithoutAlg = append(rvrsWithoutAlg, rvr.Name)
			continue
		}

		index := slices.Index(algorithms, unsupportedAlg)
		if index == -1 {
			// Unknown algorithm - log warning but ignore for algorithm selection
			// This is unlikely but possible if algorithm list changes (e.g., algorithm removed or renamed)
			rvrsWithUnknownAlg = append(rvrsWithUnknownAlg, rvr.Name)
			log.V(1).Info("Unknown algorithm in RVR error, ignoring for algorithm selection",
				"rv", rv.Name,
				"rvr", rvr.Name,
				"unknownAlg", unsupportedAlg,
				"knownAlgorithms", algorithms)
			continue
		}

		if index > maxFailedIndex {
			maxFailedIndex = index
			maxFailedRVR = rvr
		}
	}

	// If no valid algorithms found in errors (all empty or unknown), we cannot determine which algorithm is unsupported
	// Log this issue and do nothing - we should not switch algorithm without knowing which one failed
	if maxFailedIndex == -1 {
		log := log.WithValues("rv", rv.Name, "failedNodes", failedNodeNames)
		if len(rvrsWithoutAlg) > 0 {
			log = log.WithValues("rvrsWithoutAlg", rvrsWithoutAlg)
		}
		if len(rvrsWithUnknownAlg) > 0 {
			log = log.WithValues("rvrsWithUnknownAlg", rvrsWithUnknownAlg)
		}
		log.V(1).Info("Cannot determine which algorithm to switch: all RVRs have empty or unknown UnsupportedAlg")
		return reconcile.Result{}, nil // Do nothing - we don't know which algorithm is unsupported
	}

	// Try next algorithm after maximum failed index
	nextIndex := maxFailedIndex + 1
	if nextIndex >= len(algorithms) {
		// All algorithms exhausted - stop trying
		// logFields: structured logging fields for debugging algorithm exhaustion
		logFields := buildAlgorithmLogFields(rv, rv.Status.DRBD.Config.SharedSecretAlg, "", maxFailedIndex, maxFailedRVR, algorithms, failedNodeNames)
		log.V(2).Info("All algorithms exhausted, cannot switch to next", logFields...)
		return reconcile.Result{}, nil
	}

	nextAlgorithm := algorithms[nextIndex]
	currentAlg := rv.Status.DRBD.Config.SharedSecretAlg

	// Log algorithm change details at V(2) for debugging (before patch)
	// logFields: structured logging fields for debugging algorithm switch preparation
	logFields := buildAlgorithmLogFields(rv, currentAlg, nextAlgorithm, maxFailedIndex, maxFailedRVR, algorithms, failedNodeNames)
	log.V(2).Info("Preparing to switch algorithm", logFields...)

	// Update RV with new algorithm and regenerate shared secret
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Initialize status if needed
	ensureRVStatusInitialized(changedRV)

	// Check if sharedSecret already exists before generating new one
	// According to spec, we should generate new secret when switching algorithm,
	// but we check for idempotency to avoid unnecessary regeneration
	if changedRV.Status.DRBD.Config.SharedSecret == "" {
		// Generate new shared secret only if it doesn't exist
		changedRV.Status.DRBD.Config.SharedSecret = uuid.New().String()
	}
	changedRV.Status.DRBD.Config.SharedSecretAlg = nextAlgorithm

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with new algorithm")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Log result of controller logic when algorithm is changed (after successful patch)
	// Short log: detailed debug already logged at V(2), this is just a summary
	log.V(1).Info("Algorithm switched", "rv", rv.Name, "from", currentAlg, "to", nextAlgorithm)
	return reconcile.Result{}, nil
}

// hasUnsupportedAlgorithmError checks if RVR has SharedSecretAlgSelectionError in drbd.errors
func hasUnsupportedAlgorithmError(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Errors == nil {
		return false
	}
	return rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil
}

// ensureRVStatusInitialized ensures that RV status structure is initialized
func ensureRVStatusInitialized(rv *v1alpha3.ReplicatedVolume) {
	if rv.Status == nil {
		rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if rv.Status.DRBD == nil {
		rv.Status.DRBD = &v1alpha3.DRBDResource{}
	}
	if rv.Status.DRBD.Config == nil {
		rv.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
	}
}
