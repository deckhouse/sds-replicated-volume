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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupDeletePrimaryWithMapper deletes a Primary DRBDResource whose DRBDMapper is
// live, and asserts the teardown is ordered rather than abandoned: the object
// disappears only after the mapper is gone, and the LLV finalizer is released.
//
// This is the one path that exercises the agent's refusal to drop its own
// finalizer while dm layers still hold the DRBD device open. Deleting a Secondary
// (SetupDeleteDiskful) cannot reach it, because a Secondary never has a mapper.
//
// Requires drbdr to publish a device — i.e. to be Primary with a Configured
// DRBDMapper, as SetupPromotePrimary guarantees. Without that the case would
// silently degrade into the Secondary delete path and assert nothing new.
func SetupDeletePrimaryWithMapper(
	e envtesting.E,
	cl client.Client,
	drbdr *v1alpha1.DRBDResource,
	llv *snc.LVMLogicalVolume,
	drbdMapperDeletedTimeout DRBDMapperDeletedTimeout,
) {
	if drbdr.Status.Device == "" {
		e.Fatalf("require: DRBDResource %q publishes no device, so deleting it would not exercise DRBDMapper teardown",
			drbdr.Name)
	}

	if err := cl.Delete(e.Context(), drbdr); err != nil {
		e.Fatalf("deleting DRBDResource %q: %v", drbdr.Name, err)
	}

	// The DRBDResource can only vanish once the agent drops its finalizer, and it
	// may not do that while a mapper exists — so observing the object gone proves
	// the gate held, and needs no separate wait.
	waitForDeletion(e, cl, drbdr, drbdMapperDeletedTimeout.Duration)
	assertDRBDMapperAbsent(e, cl, drbdr.Name)
	assertLLVHasNoAgentFinalizer(e, cl, llv.Name)
}
