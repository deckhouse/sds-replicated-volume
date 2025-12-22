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

package rvstatusconfigdeviceminor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !v1alpha1.HasControllerFinalizer(rv) {
		log.Info("ReplicatedVolume does not have controller finalizer, skipping")
		return reconcile.Result{}, nil
	}

	// List all RVs to collect used deviceMinors
	rvList := &v1alpha1.ReplicatedVolumeList{}
	if err := r.cl.List(ctx, rvList); err != nil {
		log.Error(err, "listing RVs")
		return reconcile.Result{}, err
	}

	// Collect used deviceMinors from all RVs and find duplicates
	deviceMinorToVolumes := make(map[uint][]string)
	for _, item := range rvList.Items {
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil && item.Status.DRBD.Config.DeviceMinor != nil {
			deviceMinor := *item.Status.DRBD.Config.DeviceMinor
			if deviceMinor >= v1alpha1.RVMinDeviceMinor && deviceMinor <= v1alpha1.RVMaxDeviceMinor {
				deviceMinorToVolumes[deviceMinor] = append(deviceMinorToVolumes[deviceMinor], item.Name)
			}
		}
	}

	// Build maps for duplicate volumes
	duplicateMessages := make(map[string]string)
	for deviceMinor, volumes := range deviceMinorToVolumes {
		if len(volumes) > 1 {
			// Found duplicate deviceMinor - mark all volumes with this deviceMinor
			// Error message format: "deviceMinor X is used by volumes: [vol1 vol2 ...]"
			errorMessage := strings.Join([]string{
				"deviceMinor",
				strconv.FormatUint(uint64(deviceMinor), 10),
				"is used by volumes: [",
				strings.Join(volumes, " "),
				"]",
			}, " ")
			for _, volumeName := range volumes {
				duplicateMessages[volumeName] = errorMessage
			}
		}
	}

	// Set/clear errors for all volumes in one pass
	// Note: We process all volumes including those with DeletionTimestamp != nil because:
	// - deviceMinor is a physical DRBD device identifier that remains in use until the volume is fully deleted
	// - We need to detect and report duplicates for all volumes using the same deviceMinor to prevent conflicts
	// - Even volumes marked for deletion can cause conflicts if a new volume gets assigned the same deviceMinor
	for _, item := range rvList.Items {
		duplicateMsg, hasDuplicate := duplicateMessages[item.Name]

		var currentErrMsg string
		hasError := false
		if item.Status != nil && item.Status.Errors != nil && item.Status.Errors.DuplicateDeviceMinor != nil {
			currentErrMsg = item.Status.Errors.DuplicateDeviceMinor.Message
			hasError = true
		}

		// Skip if no change needed:
		// 1) no duplicate and no error
		if !hasDuplicate && !hasError {
			continue
		}

		// 2) duplicate exists, error exists, and message is already up-to-date
		if hasDuplicate && hasError && currentErrMsg == duplicateMsg {
			continue
		}

		// Prepare patch to set/clear error
		from := client.MergeFrom(&item)
		changedRV := item.DeepCopy()
		if changedRV.Status == nil {
			changedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
		}
		if changedRV.Status.Errors == nil {
			changedRV.Status.Errors = &v1alpha1.ReplicatedVolumeStatusErrors{}
		}

		if hasDuplicate {
			// Set error for duplicate
			changedRV.Status.Errors.DuplicateDeviceMinor = &v1alpha1.MessageError{
				Message: duplicateMsg,
			}
		} else {
			// Clear error - no longer has duplicate
			changedRV.Status.Errors.DuplicateDeviceMinor = nil
		}

		if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
			if hasDuplicate {
				log.Error(err, "Patching ReplicatedVolume status with duplicate error", "volume", item.Name)
			} else {
				log.Error(err, "Patching ReplicatedVolume status to clear duplicate error", "volume", item.Name)
			}
			continue
		}
	}

	// Check if deviceMinor already assigned and valid for this RV
	// Note: DeviceMinor is *uint, so we check if Config exists, pointer is not nil, and value is in valid range
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil && rv.Status.DRBD.Config.DeviceMinor != nil {
		deviceMinor := *rv.Status.DRBD.Config.DeviceMinor
		if deviceMinor >= v1alpha1.RVMinDeviceMinor && deviceMinor <= v1alpha1.RVMaxDeviceMinor {
			log.V(1).Info("deviceMinor already assigned and valid", "deviceMinor", deviceMinor)
			return reconcile.Result{}, nil
		}
	}

	// Find first available deviceMinor (minimum free value)
	var availableDeviceMinor uint
	found := false
	for i := v1alpha1.RVMinDeviceMinor; i <= v1alpha1.RVMaxDeviceMinor; i++ {
		if _, exists := deviceMinorToVolumes[i]; !exists {
			availableDeviceMinor = i
			found = true
			break
		}
	}

	if !found {
		// All deviceMinors are used - this is extremely unlikely (1,048,576 volumes),
		// but we should handle it gracefully
		err := fmt.Errorf(
			"no available deviceMinor for volume %s (all %d deviceMinors are used)",
			rv.Name,
			int(v1alpha1.RVMaxDeviceMinor-v1alpha1.RVMinDeviceMinor)+1,
		)
		log.Error(err, "no available deviceMinor for volume", "maxDeviceMinors", int(v1alpha1.RVMaxDeviceMinor-v1alpha1.RVMinDeviceMinor)+1)
		return reconcile.Result{}, err
	}

	// Patch RV status with assigned deviceMinor
	from := client.MergeFrom(rv)
	changedRV := rv.DeepCopy()
	if changedRV.Status == nil {
		changedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	if changedRV.Status.DRBD == nil {
		changedRV.Status.DRBD = &v1alpha1.DRBDResource{}
	}
	if changedRV.Status.DRBD.Config == nil {
		changedRV.Status.DRBD.Config = &v1alpha1.DRBDResourceConfig{}
	}
	changedRV.Status.DRBD.Config.DeviceMinor = &availableDeviceMinor

	if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
		log.Error(err, "Patching ReplicatedVolume status with deviceMinor")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("assigned deviceMinor to RV", "deviceMinor", availableDeviceMinor)

	return reconcile.Result{}, nil
}
