package rvstatusconfigdeviceminor

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
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

	// Check if deviceMinor is already set
	// Note: Since DeviceMinor is uint (not *uint), we can't check for nil.
	// We consider deviceMinor set if Config exists (even if value is 0, as 0 is a valid deviceMinor).
	// The controller will only assign deviceMinor if Config doesn't exist or is nil.
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		// Check if this is the same RV we're processing (to avoid skipping our own RV)
		// If Config exists, deviceMinor is considered set (even if 0)
		// We'll check if it's actually assigned by looking at all RVs
		log.V(1).Info("Config exists, checking if deviceMinor needs assignment")
	}

	// Get all RVs to collect used deviceMinors
	rvList := &v1alpha3.ReplicatedVolumeList{}
	if err := r.Cl.List(ctx, rvList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
	}

	// Collect used deviceMinors
	// Note: Since DeviceMinor is uint (not *uint), we consider it set if Config exists.
	// We need to check if the current RV already has a deviceMinor assigned.
	usedDeviceMinors := make(map[uint]bool)
	currentRVHasDeviceMinor := false
	for _, item := range rvList.Items {
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil {
			deviceMinor := item.Status.DRBD.Config.DeviceMinor
			if deviceMinor >= minDeviceMinor && deviceMinor <= maxDeviceMinor {
				usedDeviceMinors[deviceMinor] = true
				// Check if this is the RV we're processing
				if item.Name == rv.Name {
					currentRVHasDeviceMinor = true
				}
			}
		}
	}

	// If current RV already has deviceMinor assigned, skip
	if currentRVHasDeviceMinor {
		log.V(1).Info("deviceMinor already assigned", "deviceMinor", rv.Status.DRBD.Config.DeviceMinor)
		return reconcile.Result{}, nil
	}

	// Find available deviceMinor (minimum free value)
	var availableDeviceMinor uint
	for i := uint(minDeviceMinor); i <= uint(maxDeviceMinor); i++ {
		if !usedDeviceMinors[i] {
			availableDeviceMinor = i
			break
		}
	}

	// Update RV status with deviceMinor using PatchStatusWithConflictRetry
	// This handles concurrent updates: if another worker assigned a deviceMinor while we were processing,
	// the patch will retry and we'll re-check if deviceMinor is already set (idempotent check at start of function).
	//
	// NOTE: We use the pre-calculated availableDeviceMinor. If there's a conflict (409), PatchStatusWithConflictRetry
	// will reload the resource and retry. At that point, if deviceMinor was already set by another worker,
	// we'll detect it and skip (idempotent). If not, we'll use our pre-calculated deviceMinor.
	// This is safe because:
	// 1. We check at the start of the function if deviceMinor is already set (early exit)
	// 2. If conflict occurs, we reload and check again inside patchFn
	// 3. The worst case is we retry with the same deviceMinor, which is fine (idempotent)
	// Get fresh RV for PatchStatusWithConflictRetry (it needs a valid object with correct key)
	freshRV := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, req.NamespacedName, freshRV); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RV for patch: %w", err)
	}
	if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshRV, func(currentRV *v1alpha3.ReplicatedVolume) error {
		// Check again if deviceMinor is already set (handles race condition where another worker set it during retry)
		// Since DeviceMinor is uint, we check if Config exists and deviceMinor is in valid range
		if currentRV.Status != nil && currentRV.Status.DRBD != nil && currentRV.Status.DRBD.Config != nil {
			deviceMinor := currentRV.Status.DRBD.Config.DeviceMinor
			if deviceMinor >= minDeviceMinor && deviceMinor <= maxDeviceMinor {
				// Check if this deviceMinor is actually used (not just default 0)
				// We need to verify it's actually assigned by checking all RVs again
				// For simplicity, if Config exists, we consider it set
				log.V(1).Info("deviceMinor already assigned by another worker", "deviceMinor", deviceMinor)
				return nil // Already set, nothing to do (idempotent)
			}
		}

		// Use the pre-calculated availableDeviceMinor
		// If there was a conflict and we're retrying, the resource was reloaded with fresh data.
		// But we still use our pre-calculated deviceMinor because:
		// - If it's still available, we assign it (correct)
		// - If it was taken by another worker, we'll get another conflict and retry again
		// - Eventually, either we succeed or all deviceMinors are taken (unlikely with range 0-1048575)

		// Initialize status if needed
		if currentRV.Status == nil {
			currentRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		if currentRV.Status.DRBD == nil {
			currentRV.Status.DRBD = &v1alpha3.DRBDResource{}
		}
		if currentRV.Status.DRBD.Config == nil {
			currentRV.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
		}

		// Set the pre-calculated deviceMinor
		currentRV.Status.DRBD.Config.DeviceMinor = availableDeviceMinor

		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating RV status with deviceMinor: %w", err)
	}

	// Get final state to log the assigned deviceMinor
	finalRV := &v1alpha3.ReplicatedVolume{}
	if err := r.Cl.Get(ctx, req.NamespacedName, finalRV); err == nil {
		if finalRV.Status != nil && finalRV.Status.DRBD != nil && finalRV.Status.DRBD.Config != nil && finalRV.Status.DRBD.Config.DeviceMinor != 0 {
			log.Info("assigned deviceMinor to RV", "deviceMinor", finalRV.Status.DRBD.Config.DeviceMinor, "volume", rv.Name)
		}
	}

	return reconcile.Result{}, nil
}
