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
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Check if deviceMinor is already set - early exit optimization
	// Note: Since DeviceMinor is uint (not *uint), we can't check for nil.
	// We consider deviceMinor set if Config exists (even if value is 0, as 0 is a valid deviceMinor).
	// If deviceMinor is already assigned, we can skip List operation entirely.
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		deviceMinor := rv.Status.DRBD.Config.DeviceMinor
		if deviceMinor >= MinDeviceMinor && deviceMinor <= MaxDeviceMinor {
			log.V(1).Info("deviceMinor already assigned", "deviceMinor", deviceMinor)
			return reconcile.Result{}, nil
		}
	}

	// Get all RVs to collect used deviceMinors
	rvList := &v1alpha3.ReplicatedVolumeList{}
	if err := r.cl.List(ctx, rvList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
	}

	// Collect used deviceMinors
	// Note: Race condition handling: If two reconciles run simultaneously and both try to assign
	// the same deviceMinor, one will get a conflict (409) on Patch. The next reconciliation
	// will recalculate usedDeviceMinors and assign a different value. This is handled by
	// returning the error from Patch and letting the reconciliation retry mechanism handle it.
	usedDeviceMinors := make(map[uint]struct{})
	for _, item := range rvList.Items {
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil {
			deviceMinor := item.Status.DRBD.Config.DeviceMinor
			if deviceMinor >= MinDeviceMinor && deviceMinor <= MaxDeviceMinor {
				usedDeviceMinors[deviceMinor] = struct{}{}
			}
		}
	}

	// Find available deviceMinor (minimum free value)
	var availableDeviceMinor uint
	for i := uint(MinDeviceMinor); i <= uint(MaxDeviceMinor); i++ {
		if _, used := usedDeviceMinors[i]; !used {
			availableDeviceMinor = i
			break
		}
	}

	// Update RV status with deviceMinor
	// Note: If there's a conflict (409) due to race condition, return error - next reconciliation will solve it
	// by recalculating usedDeviceMinors and assigning a different value.
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
