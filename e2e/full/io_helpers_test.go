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
	"fmt"
	"time"

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

// ioFreezeSettleTimeout bounds the wait for a provoked freeze to show up, and
// ioFreezeSettlePoll how often the node is asked. The wait covers the whole
// chain the scenario is about: DRBD's own timers declaring the peers dead, the
// quorum re-evaluation that follows, and finally MaxHeartbeatGap elapsing with
// no write landing. The spec's context caps it, so a run with a raised
// E2E_TIMEOUT_MULTIPLIER is bounded by the scaled SpecTimeout as usual.
const (
	ioFreezeSettleTimeout = 6 * time.Minute
	ioFreezeSettlePoll    = 5 * time.Second
)

// ioFroze asserts that the data path STOPPED and that the writer blocked rather
// than died, and returns the status that established the freeze.
//
// It is the positive half of a declared freeze (fw.IOWorkload.DeclareFreeze):
// declaring one only removes the framework's veto, so the spec still has to
// prove the freeze happened. A run in which the isolated replica sailed on
// writing fails here — which is the whole point of the scenario, since a volume
// that keeps serving I/O without quorum is the defect being hunted.
//
// Terminated == nil is not a formality either. With `on-no-quorum: suspend-io`
// the writer must BLOCK; a writer that exited means the volume answered with
// errors instead of freezing (the `io-error` behaviour), which is a different
// and separately reportable defect.
func ioFroze(ctx SpecContext, w *fw.IOWorkload) fw.IOWorkloadStatus {
	GinkgoHelper()

	var frozen fw.IOWorkloadStatus
	Eventually(ctx, func() error {
		st := w.Observe(ctx)
		switch {
		case st.Terminated != nil:
			StopTrying(fmt.Sprintf(
				"io workload %s terminated instead of blocking: with on-no-quorum=suspend-io the"+
					" writer must be frozen, and an exit means the device answered with errors: %s",
				w.RunID(), st)).Now()
		case !st.Running:
			StopTrying(fmt.Sprintf("io workload %s is no longer running: %s", w.RunID(), st)).Now()
		case !st.Stalled:
			return fmt.Errorf("io workload %s is still writing: %s", w.RunID(), st)
		}
		frozen = st
		return nil
	}).WithTimeout(ioFreezeSettleTimeout).WithPolling(ioFreezeSettlePoll).Should(Succeed(),
		"io workload %s never stopped writing, so the replica kept serving I/O without quorum", w.RunID())

	// The freeze has to HOLD: a single stalled snapshot could be a slow probe,
	// while a sequence that did not move between two reads is a stopped data
	// path.
	after := w.Observe(ctx)
	Expect(after.Terminated).To(BeNil(), "io workload %s terminated during the freeze: %s", w.RunID(), after)
	Expect(after.Running).To(BeTrue(), "io workload %s stopped running during the freeze: %s", w.RunID(), after)
	Expect(after.LastSequence).To(Equal(frozen.LastSequence),
		"io workload %s advanced from sequence %d to %d while it was supposed to be frozen: %s",
		w.RunID(), frozen.LastSequence, after.LastSequence, after)
	return frozen
}

// ioResumed asserts the writer picked its work up again after the freeze that
// ioFroze established, and returns the new baseline.
//
// Because every beat is a write, an fdatasync, a read-back and a checksum
// comparison, the sequence advancing is itself the proof that the data path is
// whole again. The historical gap is deliberately NOT re-checked here: the
// freeze was declared up front and proven by ioFroze, so failing on it now
// would only fail the spec for the outcome it was written to demonstrate.
func ioResumed(ctx SpecContext, w *fw.IOWorkload, frozen fw.IOWorkloadStatus) fw.IOWorkloadStatus {
	GinkgoHelper()

	st := w.AwaitProgress(ctx, ioContinuityWrites)
	Expect(st.Terminated).To(BeNil(), "io workload %s terminated instead of resuming: %s", w.RunID(), st)
	Expect(st.Running).To(BeTrue(), "io workload %s is not running: %s", w.RunID(), st)
	Expect(st.Stalled).To(BeFalse(), "io workload %s is still stalled: %s", w.RunID(), st)
	Expect(st.LastSequence).To(BeNumerically(">=", frozen.LastSequence+ioContinuityWrites),
		"io workload %s did not resume past the sequence %d it froze at: %s",
		w.RunID(), frozen.LastSequence, st)
	return st
}
