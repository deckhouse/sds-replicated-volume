package rvstatusconfigdeviceminor

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
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
	if err := r.cl.List(ctx, rvList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
	}

	// Collect used deviceMinors
	// Note: Since DeviceMinor is uint (not *uint), we consider it set if Config exists.
	// We need to check if the current RV already has a deviceMinor assigned.
	usedDeviceMinors := make(map[uint]struct{})
	currentRVHasDeviceMinor := false
	for _, item := range rvList.Items {
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil {
			deviceMinor := item.Status.DRBD.Config.DeviceMinor
			if deviceMinor >= minDeviceMinor && deviceMinor <= maxDeviceMinor {
				usedDeviceMinors[deviceMinor] = struct{}{}
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
		if _, used := usedDeviceMinors[i]; !used {
			availableDeviceMinor = i
			break
		}
	}

	// Update RV status with deviceMinor
	// If there's a conflict (409), return error - next reconciliation will solve it
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()
	if changedRV.Status == nil {
		changedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}
	if changedRV.Status.DRBD == nil {
		changedRV.Status.DRBD = &v1alpha3.DRBDResource{}
	}
	if changedRV.Status.DRBD.Config == nil {
		changedRV.Status.DRBD.Config = &v1alpha3.DRBDResourceConfig{}
	}
	changedRV.Status.DRBD.Config.DeviceMinor = availableDeviceMinor

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with deviceMinor")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("assigned deviceMinor to RV", "deviceMinor", availableDeviceMinor, "volume", rv.Name)

	return reconcile.Result{}, nil
}
