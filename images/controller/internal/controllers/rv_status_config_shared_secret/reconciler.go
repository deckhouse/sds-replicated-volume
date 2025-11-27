package rvstatusconfigsharedsecret

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	Cl     client.Client
	Log    *slog.Logger
	LogAlt logr.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

const (
	// Shared secret hashing algorithms in order of preference
	algorithmSHA256 = "sha256"
	algorithmSHA1   = "sha1"
)

var algorithms = []string{algorithmSHA256, algorithmSHA1}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.LogAlt.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Get the RV
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("RV not found, might be deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting RV %s: %w", req.NamespacedName, err)
	}

	// Check if sharedSecret is not set - generate new one
	if rv.Status == nil || rv.Status.Config == nil || rv.Status.Config.SharedSecret == "" {
		return r.generateSharedSecret(ctx, rv, log)
	}

	// Check for UnsupportedAlgorithm errors in RVRs
	return r.handleUnsupportedAlgorithm(ctx, rv, log)
}

// generateSharedSecret generates a new shared secret and selects the first algorithm
func (r *Reconciler) generateSharedSecret(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Generate new shared secret
	sharedSecret := uuid.New().String()
	algorithm := algorithms[0] // Start with first algorithm (sha256)

	log.Info("Generating new shared secret", "algorithm", algorithm)

	// Update RV status
	rvKey := client.ObjectKeyFromObject(rv)
	freshRV := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, rvKey, freshRV); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RV for patch: %w", err)
	}

	if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshRV, func(currentRV *v1alpha3.ReplicatedVolume) error {
		// Check again if sharedSecret is already set (handles race condition)
		if currentRV.Status != nil && currentRV.Status.Config != nil && currentRV.Status.Config.SharedSecret != "" {
			log.V(1).Info("sharedSecret already set by another worker")
			return nil // Already set, nothing to do (idempotent)
		}

		// Initialize status if needed
		if currentRV.Status == nil {
			currentRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if currentRV.Status.Config == nil {
			currentRV.Status.Config = &v1alpha3.DRBDResourceConfig{}
		}

		// Set shared secret and algorithm
		currentRV.Status.Config.SharedSecret = sharedSecret
		currentRV.Status.Config.SharedSecretAlg = algorithm

		// Initialize conditions if needed
		if currentRV.Status.Conditions == nil {
			currentRV.Status.Conditions = []metav1.Condition{}
		}

		// Set SharedSecretAlgorithmSelected condition to True
		cond := metav1.Condition{
			Type:               "SharedSecretAlgorithmSelected",
			Status:             metav1.ConditionTrue,
			Reason:             "AlgorithmSelected",
			Message:            fmt.Sprintf("Selected algorithm: %s", algorithm),
			ObservedGeneration: currentRV.Generation,
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&currentRV.Status.Conditions, cond)

		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating RV status with shared secret: %w", err)
	}

	log.Info("Generated shared secret", "algorithm", algorithm)
	return reconcile.Result{}, nil
}

// handleUnsupportedAlgorithm checks RVRs for UnsupportedAlgorithm errors and switches to next algorithm
func (r *Reconciler) handleUnsupportedAlgorithm(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
) (reconcile.Result, error) {
	// Get all RVRs for this RV
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.Cl.List(ctx, rvrList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVRs: %w", err)
	}

	// Filter by replicatedVolumeName and check for UnsupportedAlgorithm errors
	var failedNodeNames []string
	var failedAlgorithm string
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name {
			continue
		}
		if hasUnsupportedAlgorithmError(&rvr) {
			failedNodeNames = append(failedNodeNames, rvr.Spec.NodeName)
			// Get the current algorithm from RV
			if rv.Status != nil && rv.Status.Config != nil {
				failedAlgorithm = rv.Status.Config.SharedSecretAlg
			}
		}
	}

	// If no errors found, nothing to do
	if len(failedNodeNames) == 0 {
		return reconcile.Result{}, nil
	}

	log.Info("Found UnsupportedAlgorithm errors", "nodes", failedNodeNames, "algorithm", failedAlgorithm)

	// Find current algorithm index
	currentAlg := rv.Status.Config.SharedSecretAlg
	currentIndex := -1
	for i, alg := range algorithms {
		if alg == currentAlg {
			currentIndex = i
			break
		}
	}

	// If current algorithm not found, start from first
	if currentIndex == -1 {
		currentIndex = 0
	}

	// Try next algorithm
	nextIndex := currentIndex + 1
	if nextIndex >= len(algorithms) {
		// All algorithms exhausted
		return r.setAlgorithmSelectionFailed(ctx, rv, log, failedNodeNames, failedAlgorithm)
	}

	nextAlgorithm := algorithms[nextIndex]
	log.Info("Switching to next algorithm", "from", currentAlg, "to", nextAlgorithm)

	// Update RV with new algorithm and regenerate shared secret
	rvKey := client.ObjectKeyFromObject(rv)
	freshRV := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, rvKey, freshRV); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RV for patch: %w", err)
	}

	if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshRV, func(currentRV *v1alpha3.ReplicatedVolume) error {
		// Initialize status if needed
		if currentRV.Status == nil {
			currentRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if currentRV.Status.Config == nil {
			currentRV.Status.Config = &v1alpha3.DRBDResourceConfig{}
		}

		// Generate new shared secret
		currentRV.Status.Config.SharedSecret = uuid.New().String()
		currentRV.Status.Config.SharedSecretAlg = nextAlgorithm

		// Initialize conditions if needed
		if currentRV.Status.Conditions == nil {
			currentRV.Status.Conditions = []metav1.Condition{}
		}

		// Set SharedSecretAlgorithmSelected condition to True
		cond := metav1.Condition{
			Type:               "SharedSecretAlgorithmSelected",
			Status:             metav1.ConditionTrue,
			Reason:             "AlgorithmSelected",
			Message:            fmt.Sprintf("Selected algorithm: %s (previous %s failed on nodes: %v)", nextAlgorithm, failedAlgorithm, failedNodeNames),
			ObservedGeneration: currentRV.Generation,
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&currentRV.Status.Conditions, cond)

		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating RV status with new algorithm: %w", err)
	}

	log.Info("Switched to new algorithm", "algorithm", nextAlgorithm)
	return reconcile.Result{}, nil
}

// setAlgorithmSelectionFailed sets SharedSecretAlgorithmSelected condition to False
func (r *Reconciler) setAlgorithmSelectionFailed(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	log logr.Logger,
	failedNodeNames []string,
	failedAlgorithm string,
) (reconcile.Result, error) {
	log.Error(nil, "All algorithms exhausted", "failedNodes", failedNodeNames, "lastAlgorithm", failedAlgorithm)

	rvKey := client.ObjectKeyFromObject(rv)
	freshRV := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, rvKey, freshRV); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RV for patch: %w", err)
	}

	if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshRV, func(currentRV *v1alpha3.ReplicatedVolume) error {
		// Initialize status if needed
		if currentRV.Status == nil {
			currentRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if currentRV.Status.Conditions == nil {
			currentRV.Status.Conditions = []metav1.Condition{}
		}

		// Set SharedSecretAlgorithmSelected condition to False
		message := fmt.Sprintf("All algorithms exhausted. Last failed algorithm: %s on nodes: %v", failedAlgorithm, failedNodeNames)
		cond := metav1.Condition{
			Type:               "SharedSecretAlgorithmSelected",
			Status:             metav1.ConditionFalse,
			Reason:             "UnableToSelectSharedSecretAlgorithm",
			Message:            message,
			ObservedGeneration: currentRV.Generation,
			LastTransitionTime: metav1.Now(),
		}
		meta.SetStatusCondition(&currentRV.Status.Conditions, cond)

		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating RV condition: %w", err)
	}

	return reconcile.Result{}, nil
}
