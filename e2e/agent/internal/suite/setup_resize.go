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
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupResize exercises DRBD online resize on a single-node diskful replica.
// Creates a diskful DRBDR + LLV, promotes to Primary (required by drbdsetup
// resize), grows the backing LLV to 2x AllocateSize, patches DRBDR spec.size,
// and asserts status.size reflects the growth.
func SetupResize(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	prefix string,
	nodeIdx int,
) {
	var drbdrConfiguredTimeout DRBDRConfiguredTimeout
	var llvCreatedTimeout LLVCreatedTimeout
	e.Options(&drbdrConfiguredTimeout, &llvCreatedTimeout)

	drbdr, llv := SetupDisklessToDiskfulReplica(e, cl, cluster, prefix, nodeIdx)

	// Mark data as UpToDate (required before promotion on a single-node resource
	// whose disk starts as Inconsistent).
	SetupInitialSync(e, cl, []*v1alpha1.DRBDResource{drbdr})

	drbdr = SetupPromotePrimary(e, cl, drbdr)

	initialSize := drbdr.Status.Size.DeepCopy()

	// spec.size is the upper device (usable) size. The LLV must be larger
	// to accommodate DRBD internal metadata. Grow the LLV to 2x AllocateSize
	// and set spec.size to 2x the current usable size — this guarantees
	// the new spec.size fits within the new LLV with room for metadata.
	newLLVSize := cluster.AllocateSize.DeepCopy()
	newLLVSize.Add(cluster.AllocateSize)

	newSpecSize := initialSize.DeepCopy()
	newSpecSize.Add(initialSize)

	// Grow LLV backing device.
	kubetesting.SetupResourcePatch(
		e.ScopeWithTimeout(llvCreatedTimeout.Duration),
		cl,
		client.ObjectKey{Name: llv.Name},
		func(l *snc.LVMLogicalVolume) { l.Spec.Size = newLLVSize.String() },
		isLLVResized(newLLVSize),
	)

	// Patch DRBDR spec.size (upper device size). Cannot use
	// SetupResourcePatch because spec.size cannot be decreased, so the
	// revert cleanup would fail validation. The parent cleanup from
	// SetupDisklessToDiskfulReplica deletes the DRBDR entirely.
	drbdr = patchDRBDRSizeAndWait(
		e.ScopeWithTimeout(drbdrConfiguredTimeout.Duration),
		cl, drbdr, &newSpecSize,
	)

	assertDRBDRConfigured(e, drbdr)
	assertDRBDRSizePopulated(e, drbdr)

	if drbdr.Status.Size.Cmp(initialSize) <= 0 {
		e.Fatalf("assert: status.size %s did not grow from initial %s",
			drbdr.Status.Size.String(), initialSize.String())
	}
}

func isLLVResized(targetSize resource.Quantity) func(*snc.LVMLogicalVolume) bool {
	return func(llv *snc.LVMLogicalVolume) bool {
		return llv.Status != nil && llv.Status.ActualSize.Cmp(targetSize) >= 0
	}
}

// patchDRBDRSizeAndWait patches spec.size on the DRBDR and waits for
// Configured condition to reach a terminal state. No revert cleanup is
// registered because spec.size cannot be decreased.
func patchDRBDRSizeAndWait(
	e envtesting.E,
	cl client.WithWatch,
	drbdr *v1alpha1.DRBDResource,
	newSize *resource.Quantity,
) *v1alpha1.DRBDResource {
	watcherScope := e.Scope()
	defer watcherScope.Close()

	wait := kubetesting.SetupResourceWatcher(watcherScope, cl, drbdr)

	base := drbdr.DeepCopy()
	drbdr.Spec.Size = newSize
	if err := cl.Patch(e.Context(), drbdr, client.MergeFrom(base)); err != nil {
		e.Fatalf("patching DRBDResource %q spec.size to %s: %v",
			drbdr.Name, newSize.String(), err)
	}

	return wait(e, drbdr, isDRBDRTerminal)
}
