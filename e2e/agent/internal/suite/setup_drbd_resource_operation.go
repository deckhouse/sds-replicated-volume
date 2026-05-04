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

package suite

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupDRBDResourceOperation creates a DRBDResourceOperation, waits for it to
// reach a terminal phase (Succeeded or Failed), asserts Succeeded, and returns
// the result.
func SetupDRBDResourceOperation(
	e envtesting.E,
	wc client.WithWatch,
	op *v1alpha1.DRBDResourceOperation,
) *v1alpha1.DRBDResourceOperation {
	result := kubetesting.SetupResource(e, wc, op, isDRBDROpTerminal)

	if result.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		e.Fatalf("assert: DRBDResourceOperation %q phase is %s (message: %s)",
			result.Name, result.Status.Phase, result.Status.Message)
	}

	return result
}

func isDRBDROpTerminal(op *v1alpha1.DRBDResourceOperation) bool {
	return op.Status.Phase == v1alpha1.DRBDOperationPhaseSucceeded ||
		op.Status.Phase == v1alpha1.DRBDOperationPhaseFailed
}
