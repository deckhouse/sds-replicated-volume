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
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/kubetesting"
)

// SetupDStateRecovery freezes I/O to the backing LV of a pre-existing diskful
// DRBDResource using `dmsetup suspend`, then verifies that the agent's
// reconcile worker pool is not exhausted by the wedged reconciles: a second
// (canary) DRBDResource on the same node must still reach Configured=True
// within the normal timeout.
//
// While the LV is suspended, every drbd I/O against it (drbdmeta dump-md
// during the observe loop, drbdsetup attach if re-tried, etc.) blocks in
// uninterruptible kernel D-state. Without ctrlexec.WithCache + WithTimeout +
// WithWaitDelay wrapping the production exec factory in
// drbdr.BuildController, those reconciles would block their worker
// goroutines indefinitely; with the wrappers, each attempt returns within
// ~drbdExecTimeout + drbdExecWaitDelay and the worker is freed.
//
// Cleanup `dmsetup resume`s the LV so the parent scope's cleanup
// (SetupDisklessToDiskfulReplica for the frozen resource) can tear it down.
// The resume runs before the canary's cleanups (LIFO ordering).
//
// Requirements (caller's responsibility):
//   - cluster.Nodes[nodeIdx]'s LVG MUST have at least 2x AllocateSize free,
//     because both the frozen LV and the canary LV are allocated against it.
//   - The agent pod on cluster.Nodes[nodeIdx] MUST have `dmsetup` available
//     and permission to suspend a device on the host.
func SetupDStateRecovery(
	e envtesting.E,
	cl client.WithWatch,
	cluster *Cluster,
	ne *kubetesting.NodeExec,
	drbdrFrozen *v1alpha1.DRBDResource,
	llvFrozen *snc.LVMLogicalVolume,
	canaryPrefix string,
	nodeIdx int,
) {
	if drbdrFrozen == nil {
		e.Fatal("require: drbdrFrozen must not be nil")
	}
	if llvFrozen == nil {
		e.Fatal("require: llvFrozen must not be nil")
	}

	node := cluster.Nodes[nodeIdx]

	// `dmsetup` accepts a device path; using the LVM symlink is robust
	// against dm-mapper name mangling (LVM doubles dashes in mapper names).
	devPath := fmt.Sprintf("/dev/%s/%s",
		node.LVG.Spec.ActualVGNameOnTheNode,
		llvFrozen.Spec.ActualLVNameOnTheNode)

	// Arrange: freeze I/O to the backing LV. After this, any drbd-side read
	// or write against drbdrFrozen's backing device blocks in D-state.
	ne.Exec(e, node.Name, "dmsetup", "suspend", devPath)
	resumed := false
	e.Cleanup(func() {
		if !resumed {
			ne.Exec(e, node.Name, "dmsetup", "resume", devPath)
		}
	})

	// Act + Assert: create a canary diskful DRBDResource on the same node.
	// Its LV is freshly allocated from the same VG, independent of the
	// suspended one, so it must reach Configured=True within the configured
	// timeout. Failure to do so means the agent's reconcile worker pool is
	// blocked by the wedged reconciles of drbdrFrozen — i.e. the D-state
	// protection (ctrlexec.WithCache + WithTimeout) is not in place or is
	// not effective.
	SetupDisklessToDiskfulReplica(e, cl, cluster, canaryPrefix, nodeIdx)

	// Resume the LV so the parent scope's frozen-DRBDR cleanup can tear it
	// down cleanly. Any accumulated D-state processes inside the agent pod
	// unblock as soon as the device resumes.
	ne.Exec(e, node.Name, "dmsetup", "resume", devPath)
	resumed = true
}
