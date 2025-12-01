package rvstatusconfigsharedsecret

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Update RV status with shared secret
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Check again if sharedSecret is already set (handles race condition)
	if changedRV.Status != nil && changedRV.Status.DRBD != nil && changedRV.Status.DRBD.Config != nil && changedRV.Status.DRBD.Config.SharedSecret != "" {
		log.V(1).Info("sharedSecret already set by another worker")
		return reconcile.Result{}, nil // Already set, nothing to do (idempotent)
	}

	// Initialize status if needed
	if changedRV.Status == nil {
		changedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if changedRV.Status.DRBD == nil {
		changedRV.Status.DRBD = &v1alpha3.DRBDResource{}
	}
	if changedRV.Status.DRBD.Config == nil {
		changedRV.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
	}

	// Set shared secret and algorithm
	changedRV.Status.DRBD.Config.SharedSecret = sharedSecret
	changedRV.Status.DRBD.Config.SharedSecretAlg = algorithm

	// Initialize conditions if needed
	if changedRV.Status.Conditions == nil {
		changedRV.Status.Conditions = []metav1.Condition{}
	}

	// Set SharedSecretAlgorithmSelected condition to True
	cond := metav1.Condition{
		Type:               "SharedSecretAlgorithmSelected",
		Status:             metav1.ConditionTrue,
		Reason:             "AlgorithmSelected",
		Message:            fmt.Sprintf("Selected algorithm: %s", algorithm),
		ObservedGeneration: changedRV.Generation,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&changedRV.Status.Conditions, cond)

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with shared secret")
		return reconcile.Result{}, client.IgnoreNotFound(err)
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
	if err := r.cl.List(ctx, rvrList); err != nil {
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
			if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
				failedAlgorithm = rv.Status.DRBD.Config.SharedSecretAlg
			}
		}
	}

	// If no errors found, nothing to do
	if len(failedNodeNames) == 0 {
		return reconcile.Result{}, nil
	}

	log.Info("Found UnsupportedAlgorithm errors", "nodes", failedNodeNames, "algorithm", failedAlgorithm)

	// Find current algorithm index
	currentAlg := rv.Status.DRBD.Config.SharedSecretAlg
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
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Initialize status if needed
	if changedRV.Status == nil {
		changedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if changedRV.Status.DRBD == nil {
		changedRV.Status.DRBD = &v1alpha3.DRBDResource{}
	}
	if changedRV.Status.DRBD.Config == nil {
		changedRV.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
	}

	// Generate new shared secret
	changedRV.Status.DRBD.Config.SharedSecret = uuid.New().String()
	changedRV.Status.DRBD.Config.SharedSecretAlg = nextAlgorithm

	// Initialize conditions if needed
	if changedRV.Status.Conditions == nil {
		changedRV.Status.Conditions = []metav1.Condition{}
	}

	// Set SharedSecretAlgorithmSelected condition to True
	cond := metav1.Condition{
		Type:               "SharedSecretAlgorithmSelected",
		Status:             metav1.ConditionTrue,
		Reason:             "AlgorithmSelected",
		Message:            fmt.Sprintf("Selected algorithm: %s (previous %s failed on nodes: %v)", nextAlgorithm, failedAlgorithm, failedNodeNames),
		ObservedGeneration: changedRV.Generation,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&changedRV.Status.Conditions, cond)

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with new algorithm")
		return reconcile.Result{}, client.IgnoreNotFound(err)
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

	// Update RV condition to False
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()

	// Initialize status if needed
	if changedRV.Status == nil {
		changedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if changedRV.Status.Conditions == nil {
		changedRV.Status.Conditions = []metav1.Condition{}
	}

	// Set SharedSecretAlgorithmSelected condition to False
	message := fmt.Sprintf("All algorithms exhausted. Last failed algorithm: %s on nodes: %v", failedAlgorithm, failedNodeNames)
	cond := metav1.Condition{
		Type:               "SharedSecretAlgorithmSelected",
		Status:             metav1.ConditionFalse,
		Reason:             "UnableToSelectSharedSecretAlgorithm",
		Message:            message,
		ObservedGeneration: changedRV.Generation,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&changedRV.Status.Conditions, cond)

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume condition")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	return reconcile.Result{}, nil
}
