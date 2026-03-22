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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func (r *OperationReconciler) executeDeleteSnapshot(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	drbdr *v1alpha1.DRBDResource,
) error {
	if op.Spec.DeleteSnapshot == nil {
		return fmt.Errorf("deleteSnapshot parameters are required")
	}

	snapshotName := op.Spec.DeleteSnapshot.SnapshotName
	if snapshotName == "" {
		return fmt.Errorf("snapshotName must not be empty")
	}

	vgName, _, err := r.resolveBackingLV(ctx, drbdr)
	if err != nil {
		return fmt.Errorf("resolving backing LV: %w", err)
	}

	snapPath := fmt.Sprintf("/dev/%s/%s", vgName, snapshotName)

	cmd := exec.CommandContext(ctx, "lvremove", "-f", snapPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lvremove failed: %w, output: %s", err, string(output))
	}

	return nil
}
