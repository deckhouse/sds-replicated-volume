/*
Copyright 2026 Flant JSC

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

package drbdrop

import (
	"context"
	"fmt"
	"os/exec"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func (r *OperationReconciler) executeCreateSnapshot(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	drbdr *v1alpha1.DRBDResource,
) (snapshotPath string, err error) {
	if op.Spec.CreateSnapshot == nil {
		return "", fmt.Errorf("createSnapshot parameters are required")
	}

	snapshotName := op.Spec.CreateSnapshot.SnapshotName
	if snapshotName == "" {
		return "", fmt.Errorf("snapshotName must not be empty")
	}

	vgName, lvName, err := r.resolveBackingLV(ctx, drbdr)
	if err != nil {
		return "", fmt.Errorf("resolving backing LV: %w", err)
	}

	lvPath := fmt.Sprintf("/dev/%s/%s", vgName, lvName)

	cmd := exec.CommandContext(ctx, "lvcreate", "--snapshot", "--name", snapshotName, lvPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("lvcreate --snapshot failed: %w, output: %s", err, string(output))
	}

	return fmt.Sprintf("/dev/%s/%s", vgName, snapshotName), nil
}

func (r *OperationReconciler) resolveBackingLV(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
) (vgName, lvName string, err error) {
	llvName := drbdr.Spec.LVMLogicalVolumeName
	if llvName == "" {
		return "", "", fmt.Errorf("DRBDResource %q has no LVMLogicalVolumeName", drbdr.Name)
	}

	llv := &snc.LVMLogicalVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: llvName}, llv); err != nil {
		return "", "", fmt.Errorf("getting LVMLogicalVolume %q: %w", llvName, err)
	}

	lvgName := llv.Spec.LVMVolumeGroupName
	if lvgName == "" {
		return "", "", fmt.Errorf("LVMLogicalVolume %q has no LVMVolumeGroupName", llvName)
	}

	lvg := &snc.LVMVolumeGroup{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: lvgName}, lvg); err != nil {
		return "", "", fmt.Errorf("getting LVMVolumeGroup %q: %w", lvgName, err)
	}

	return lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode, nil
}
