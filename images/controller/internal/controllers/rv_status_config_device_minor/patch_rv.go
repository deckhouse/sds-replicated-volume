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

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func patchDupErr(ctx context.Context, cl client.Client, rv *v1alpha1.ReplicatedVolume, conflictingRVNames []string) error {
	return patchRV(ctx, cl, rv, fmt.Sprintf("duplicate device minor, used in RVs: %s", conflictingRVNames), nil)
}

func patchRV(ctx context.Context, cl client.Client, rv *v1alpha1.ReplicatedVolume, msg string, dm *DeviceMinor) error {
	orig := client.MergeFrom(rv.DeepCopy())

	changeRVErr(rv, msg)
	if dm != nil {
		changeRVDM(rv, *dm)
	}

	if err := cl.Status().Patch(ctx, rv, orig); err != nil {
		return fmt.Errorf("patching rv.status.errors.deviceMinor: %w", err)
	}

	return nil
}

func changeRVErr(rv *v1alpha1.ReplicatedVolume, msg string) {
	if msg == "" {
		if rv.Status == nil || rv.Status.Errors == nil || rv.Status.Errors.DeviceMinor == nil {
			return
		}
		rv.Status.Errors.DeviceMinor = nil
	} else {
		if rv.Status == nil {
			rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
		}
		if rv.Status.Errors == nil {
			rv.Status.Errors = &v1alpha1.ReplicatedVolumeStatusErrors{}
		}
		rv.Status.Errors.DeviceMinor = &v1alpha1.MessageError{Message: msg}
	}

}

func changeRVDM(rv *v1alpha1.ReplicatedVolume, dm DeviceMinor) {
	if rv.Status == nil {
		rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	if rv.Status.DRBD == nil {
		rv.Status.DRBD = &v1alpha1.DRBDResource{}
	}
	if rv.Status.DRBD.Config == nil {
		rv.Status.DRBD.Config = &v1alpha1.DRBDResourceConfig{}
	}
	rv.Status.DRBD.Config.DeviceMinor = u.Ptr(uint(dm))
}
