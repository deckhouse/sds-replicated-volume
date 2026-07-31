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

package full

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
)

// ioContinuityWrites is how many verified writes a spec demands to accept that
// the data path is alive at a given point. Every write is a device write that
// was fdatasync-ed, read back and checksum-verified, so a handful of them is
// enough evidence — and small enough not to dominate the spec's budget.
const ioContinuityWrites = 5

// devicePathPublished matches an RVA whose device is available on the node.
// The workload needs the published path, not merely an Attached condition.
func devicePathPublished() types.GomegaMatcher {
	return match.RVA.Custom("device path published",
		func(rva *v1alpha1.ReplicatedVolumeAttachment) bool {
			return rva.Status.DevicePath != ""
		})
}

// startVolumeIO starts a raw-device writer on the node trva is attached to and
// returns the handle; its cleanup is already registered by the framework.
//
// The device comes from RVA.status.devicePath and the expected identity from
// the DRBD resource of the replica living on that same node, so the writer
// refuses to touch anything but this volume's device. The caller MUST hold
// fw.LabelDisruptive, on the spec or on an enclosing container: the writer
// writes to a raw block device on the host. The requirement is enforced by
// fw.StartIOWorkload itself, which fails the spec before the writer is started.
// This wrapper adds no check of its own: everything it does before that call is
// a read, so a second guard here would only be a second wording of the same
// refusal, free to drift from the first.
//
// tune adjusts the options before the writer starts — a spec that disrupts the
// cluster raises MaxHeartbeatGap so a brief blip is not read as a stall.
func startVolumeIO(
	ctx SpecContext,
	trv *fw.TestRV,
	trva *fw.TestRVA,
	tune ...func(*fw.IOWorkloadOptions),
) *fw.IOWorkload {
	GinkgoHelper()

	trva.Await(ctx, devicePathPublished())
	rva := trva.Object()
	node := rva.Spec.NodeName
	Expect(node).NotTo(BeEmpty(), "attachment %s carries no node name", trva.Name())

	opts := fw.IOWorkloadOptions{
		NodeName:         node,
		DevicePath:       rva.Status.DevicePath,
		DRBDResourceName: rvrOnNode(trv, node).DRBDResourceName(),
	}
	for _, t := range tune {
		t(&opts)
	}
	return f.StartIOWorkload(ctx, opts)
}

// ioAlive asserts the writer is running and has already produced a verified
// write, and returns that baseline status.
func ioAlive(ctx SpecContext, w *fw.IOWorkload) fw.IOWorkloadStatus {
	GinkgoHelper()
	st := w.Observe(ctx)
	Expect(st.Terminated).To(BeNil(), "io workload %s terminated: %s", w.RunID(), st)
	Expect(st.Running).To(BeTrue(), "io workload %s is not running: %s", w.RunID(), st)
	Expect(st.Stalled).To(BeFalse(), "io workload %s stalled: %s", w.RunID(), st)
	Expect(st.GapExceeded).To(BeFalse(), "io workload %s stalled earlier in the run: %s", w.RunID(), st)
	Expect(st.LastSequence).To(BeNumerically(">=", 0),
		"io workload %s has not verified a single write: %s", w.RunID(), st)
	return st
}

// ioSustainedWrites is the longer stretch a spec demands when it needs real
// elapsed time — for instance to show that a reported state is stable rather
// than slowly converging, with the RV's continuous invariants running on every
// snapshot that arrives meanwhile.
const ioSustainedWrites = 60

// ioProgressed asserts the writer kept writing since prev: it waits for
// ioContinuityWrites more verified writes and requires the sequence to have
// actually advanced past prev, with no stall and no termination on the way.
//
// The returned status is the new baseline for the next window, so a spec
// chains ioProgressed calls around the events it wants I/O continuity through.
func ioProgressed(ctx SpecContext, w *fw.IOWorkload, prev fw.IOWorkloadStatus) fw.IOWorkloadStatus {
	GinkgoHelper()
	return ioProgressedBy(ctx, w, prev, ioContinuityWrites)
}

// ioProgressedBy is ioProgressed with an explicit number of writes to wait for.
func ioProgressedBy(
	ctx SpecContext,
	w *fw.IOWorkload,
	prev fw.IOWorkloadStatus,
	writes int64,
) fw.IOWorkloadStatus {
	GinkgoHelper()

	st := w.AwaitProgress(ctx, writes)
	Expect(st.Terminated).To(BeNil(), "io workload %s terminated: %s", w.RunID(), st)
	Expect(st.Running).To(BeTrue(), "io workload %s is not running: %s", w.RunID(), st)
	Expect(st.Stalled).To(BeFalse(), "io workload %s stalled: %s", w.RunID(), st)
	Expect(st.GapExceeded).To(BeFalse(), "io workload %s stalled earlier in the run: %s", w.RunID(), st)
	Expect(st.LastSequence).To(BeNumerically(">=", prev.LastSequence+writes),
		"io workload %s did not advance past sequence %d: %s", w.RunID(), prev.LastSequence, st)
	return st
}
