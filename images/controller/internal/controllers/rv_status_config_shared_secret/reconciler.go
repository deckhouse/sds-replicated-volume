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

	// Check if sharedSecret is not set - generate new one
	// Filtering through early return instead of WithEventFilter
	if rv.Status == nil || rv.Status.DRBD == nil || rv.Status.DRBD.Config == nil || rv.Status.DRBD.Config.SharedSecret == "" {
		return r.reconcileGenerateSharedSecret(ctx, rv, log)
	}

	// Check for UnsupportedAlgorithm errors in RVRs
	return r.reconcileHandleUnsupportedAlgorithm(ctx, rv, log)
}

// reconcileGenerateSharedSecret generates a new shared secret and selects the first algorithm
func (r *Reconciler) reconcileGenerateSharedSecret(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Generate new shared secret
	sharedSecret := uuid.New().String()
	algorithm := v1alpha3.SharedSecretAlgorithms()[0] // Start with first algorithm (sha256)

	log.Info("Generating new shared secret", "algorithm", algorithm)

	// Update RV status with shared secret
	// If there's a conflict (409), return error - next reconciliation will solve it
	// Race condition handling: If two reconciles run simultaneously, one will get 409 Conflict on Patch.
	// The next reconciliation will check if sharedSecret is already set and skip generation.
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Check if sharedSecret is already set (idempotent check after DeepCopy)
	// Note: This check is on the copy, but if there's a race condition, Patch will return 409 Conflict
	// and the next reconciliation will handle it correctly.
	if changedRV.Status != nil && changedRV.Status.DRBD != nil && changedRV.Status.DRBD.Config != nil && changedRV.Status.DRBD.Config.SharedSecret != "" {
		log.V(1).Info("sharedSecret already set and valid", "algorithm", changedRV.Status.DRBD.Config.SharedSecretAlg)
		return reconcile.Result{}, nil // Already set, nothing to do (idempotent)
	}

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

// reconcileHandleUnsupportedAlgorithm checks RVRs for UnsupportedAlgorithm errors and switches to next algorithm
func (r *Reconciler) reconcileHandleUnsupportedAlgorithm(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Get all RVRs for this RV
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Filter by replicatedVolumeName and check for UnsupportedAlgorithm errors
	var failedNodeNames []string
	var failedAlgorithm string
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name {
			continue
		}
		if HasUnsupportedAlgorithmError(&rvr) {
			failedNodeNames = append(failedNodeNames, rvr.Spec.NodeName)
			// Get the unsupported algorithm from RVR error
			if rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Errors != nil &&
				rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError != nil {
				failedAlgorithm = rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg
			}
		}
	}

	// If no errors found, nothing to do
	if len(failedNodeNames) == 0 {
		return reconcile.Result{}, nil
	}

	log.Info("Found UnsupportedAlgorithm errors", "nodes", failedNodeNames, "algorithm", failedAlgorithm)

	// Find current algorithm index
	algorithms := v1alpha3.SharedSecretAlgorithms()
	currentAlg := rv.Status.DRBD.Config.SharedSecretAlg
	currentIndex := slices.Index(algorithms, currentAlg)

	// If current algorithm not found, start from first
	if currentIndex == -1 {
		currentIndex = 0
	}

	// Try next algorithm
	nextIndex := currentIndex + 1
	if nextIndex >= len(algorithms) {
		// All algorithms exhausted - stop trying
		log.Info("All algorithms exhausted", "failedNodes", failedNodeNames, "lastAlgorithm", failedAlgorithm)
		return reconcile.Result{}, nil
	}

	nextAlgorithm := algorithms[nextIndex]
	log.Info("Switching to next algorithm", "from", currentAlg, "to", nextAlgorithm)

	// Update RV with new algorithm and regenerate shared secret
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Initialize status if needed
	ensureRVStatusInitialized(changedRV)

	// Generate new shared secret
	changedRV.Status.DRBD.Config.SharedSecret = uuid.New().String()
	changedRV.Status.DRBD.Config.SharedSecretAlg = nextAlgorithm

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with new algorithm")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Switched to new algorithm")
	return reconcile.Result{}, nil
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
