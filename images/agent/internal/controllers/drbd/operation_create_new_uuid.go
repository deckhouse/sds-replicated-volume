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

package drbd

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

// executeCreateNewUUID executes the CreateNewUUID operation.
func (r *OperationReconciler) executeCreateNewUUID(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	drbdr *v1alpha1.DRBDResource,
) error {
	// Get minor number from DRBD status
	drbdResName := DRBDResourceNameOnTheNode(drbdr)
	aState, err := observeActualDRBDState(ctx, drbdResName)
	if err != nil {
		return fmt.Errorf("observing DRBD state: %w", err)
	}
	if aState.IsZero() || !aState.ResourceExists() {
		return fmt.Errorf("DRBD resource %q does not exist", drbdResName)
	}

	volumes := aState.Volumes()
	if len(volumes) == 0 {
		return fmt.Errorf("DRBD resource %q has no volumes", drbdResName)
	}

	minor := uint(volumes[0].Minor())

	// Get parameters
	clearBitmap := false
	if op.Spec.CreateNewUUID != nil {
		clearBitmap = op.Spec.CreateNewUUID.ClearBitmap
	}

	// Execute command
	if err := drbdsetup.ExecuteNewCurrentUUID(ctx, minor, clearBitmap, false); err != nil {
		return fmt.Errorf("executing new-current-uuid: %w", err)
	}

	return nil
}
