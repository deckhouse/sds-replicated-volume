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

package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testRunID       = "e2e-run-1-io"
	testNode        = "worker-1"
	testDevicePath  = "/dev/sdsrv/rv-1-0"
	testDRBDName    = "sdsrv-rv-1-0"
	testMinor       = 1012
	testWriterPID   = 5150
	testWriterStart = "918273"
)

// fakeIONode models the node the workload runs on: the marker file, the
// process behind it, the heartbeat journal, and the node clock. It answers the
// exec commands the workload issues, so the whole lifecycle can be driven
// without a cluster.
type fakeIONode struct {
	bootID string
	nowMS  int64

	marker   *ioMarker
	procStat string
	journal  []string
	sequence int64

	statusJSON   string
	statusErr    error
	canonical    string
	readlinkExit int

	spawnErr   error
	spawnExit  int
	onSpawn    func(n *fakeIONode)
	beatOnPeek bool
	ignoreTerm bool

	writerStarts  int
	refusedSpawns int
	signals       []string
	purged        bool
}

func newFakeIONode() *fakeIONode {
	return &fakeIONode{
		bootID:     "boot-a",
		nowMS:      1750000000000,
		statusJSON: fmt.Sprintf(`[{"name":%q,"devices":[{"volume":0,"minor":%d}]}]`, testDRBDName, testMinor),
		canonical:  fmt.Sprintf("/dev/drbd%d\n", testMinor),
	}
}

// startWriter models the writer's own start: it publishes the marker only if
// no marker exists (link(2) with EEXIST semantics), so a repeated spawn can
// never produce a second writer.
func (n *fakeIONode) startWriter() {
	if n.marker != nil {
		n.refusedSpawns++
		return
	}
	n.writerStarts++
	n.marker = &ioMarker{
		RunID:         testRunID,
		PID:           testWriterPID,
		ProcStartTime: testWriterStart,
		BootID:        n.bootID,
		Device:        testDevicePath,
	}
	n.procStat = procStatLine(testWriterPID, testWriterStart)
	// The writer owns the journal from scratch, as the program does once it
	// holds the marker; its sequence starts at 0 again.
	n.journal = []string{
		fmt.Sprintf("start %d %d %s 147:%d 1048576", n.nowMS, testWriterPID, testDevicePath, testMinor),
	}
	n.sequence = 0
	n.beat()
}

// startAndFailIdentity models a writer that published its marker, opened the
// device and found another volume behind the descriptor: it fails without
// writing a single record.
func (n *fakeIONode) startAndFailIdentity() {
	if n.marker != nil {
		n.refusedSpawns++
		return
	}
	n.writerStarts++
	n.journal = []string{fmt.Sprintf(
		"fail %d identity: %s is 147:2024, expected 147:%d", n.nowMS, testDevicePath, testMinor)}
	n.sequence = 0
	n.marker = nil
	n.procStat = ""
}

func (n *fakeIONode) beat() {
	n.nowMS += 200
	n.journal = append(n.journal, fmt.Sprintf("ok %d %d %d a1b2c3d4", n.sequence, n.sequence%16, n.nowMS))
	n.sequence++
}

func (n *fakeIONode) failIO(message string) {
	n.nowMS += 200
	n.journal = append(n.journal, fmt.Sprintf("fail %d io: %s", n.nowMS, message))
	n.marker = nil
	n.procStat = ""
}

func (n *fakeIONode) probeOutput() string {
	marker := ""
	if n.marker != nil {
		raw, err := json.Marshal(n.marker)
		Expect(err).NotTo(HaveOccurred())
		marker = string(raw)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#now %d\n#boot %s\n#marker %s\n#proc %s\n#journal\n", n.nowMS, n.bootID, marker, n.procStat)
	for _, line := range n.journal {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (n *fakeIONode) respond(call execCall) (ExecResult, error) {
	switch {
	case call.Kind == execKindDrbdsetup:
		return ExecResult{Stdout: n.statusJSON}, n.statusErr

	case strings.HasPrefix(call.Display, "readlink -f"):
		return ExecResult{Stdout: n.canonical, ExitCode: n.readlinkExit}, nil

	case strings.HasPrefix(call.Display, "io-workload spawn"):
		if n.onSpawn != nil {
			n.onSpawn(n)
		}
		return ExecResult{Stdout: spawnedMarker + "\n", ExitCode: n.spawnExit}, n.spawnErr

	case strings.HasPrefix(call.Display, "io-workload probe"):
		if n.beatOnPeek && n.marker != nil {
			n.beat()
		}
		return ExecResult{Stdout: n.probeOutput()}, nil

	case strings.HasPrefix(call.Display, "io-workload signal"):
		sig := strings.Fields(call.Display)[2]
		n.signals = append(n.signals, sig)
		if sig == "TERM" && n.ignoreTerm {
			return ExecResult{Stdout: "#signal sent\n"}, nil
		}
		if n.marker != nil {
			n.nowMS += 100
			n.journal = append(n.journal, fmt.Sprintf("stopped %d %d", n.sequence, n.nowMS))
			n.marker = nil
			n.procStat = ""
		}
		return ExecResult{Stdout: "#signal sent\n"}, nil

	case strings.HasPrefix(call.Display, "io-workload clear-stale-marker"):
		n.marker = nil
		n.procStat = ""
		n.journal = nil
		return ExecResult{Stdout: "#cleared\n"}, nil

	case strings.HasPrefix(call.Display, "io-workload purge"):
		n.purged = true
		n.marker = nil
		n.procStat = ""
		n.journal = nil
		return ExecResult{Stdout: "#purged\n"}, nil
	}

	Fail("unexpected command: " + call.Display)
	return ExecResult{}, nil
}

// newTestIOWorkload wires a workload to the fake node. Polling is fast so
// negative cases finish quickly.
func newTestIOWorkload(node *fakeIONode) (*IOWorkload, *stubRunner) {
	stub := &stubRunner{respond: node.respond}
	return workloadOn(&Framework{nodeRun: stub}), stub
}

func workloadOn(f *Framework) *IOWorkload {
	w, err := f.newIOWorkload(IOWorkloadOptions{
		NodeName:         testNode,
		DevicePath:       testDevicePath,
		DRBDResourceName: testDRBDName,
		RunID:            testRunID,
		StartTimeout:     100 * time.Millisecond,
		StopTimeout:      100 * time.Millisecond,
	})
	Expect(err).NotTo(HaveOccurred())
	w.poll = time.Millisecond
	return w
}

var _ = Describe("IOWorkload options", func() {
	f := &Framework{}

	DescribeTable("refuses options that cannot produce a safe writer",
		func(mutate func(o *IOWorkloadOptions), wantMsg string) {
			opts := IOWorkloadOptions{
				NodeName:         testNode,
				DevicePath:       testDevicePath,
				DRBDResourceName: testDRBDName,
				RunID:            testRunID,
			}
			mutate(&opts)

			_, err := f.newIOWorkload(opts)

			Expect(err).To(MatchError(ContainSubstring(wantMsg)))
		},
		Entry("no node", func(o *IOWorkloadOptions) { o.NodeName = "" }, "NodeName must not be empty"),
		Entry("no device", func(o *IOWorkloadOptions) { o.DevicePath = "" }, "DevicePath must not be empty"),
		Entry("device outside /dev", func(o *IOWorkloadOptions) { o.DevicePath = "/tmp/x" }, "not a plain /dev path"),
		Entry("device path with shell metacharacters", func(o *IOWorkloadOptions) { o.DevicePath = "/dev/x;rm -rf /" }, "not a plain /dev path"),
		Entry("no drbd resource", func(o *IOWorkloadOptions) { o.DRBDResourceName = "" }, "DRBDResourceName must not be empty"),
		Entry("run id with shell metacharacters", func(o *IOWorkloadOptions) { o.RunID = "run 1;id" }, "RunID"),
		Entry("degenerate ring", func(o *IOWorkloadOptions) { o.Slots = 1 }, "Slots must be at least 2"),
		Entry("unaligned slot", func(o *IOWorkloadOptions) { o.SlotSize = 1000 }, "multiple of 512"),
	)

	It("applies the defaults", func() {
		w, err := f.newIOWorkload(IOWorkloadOptions{
			NodeName:         testNode,
			DevicePath:       testDevicePath,
			DRBDResourceName: testDRBDName,
			RunID:            testRunID,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(w.opts.Slots).To(Equal(ioWorkloadDefaultSlots))
		Expect(w.opts.SlotSize).To(Equal(ioWorkloadDefaultSlotSize))
		Expect(w.opts.MaxHeartbeatGap).To(Equal(ioWorkloadDefaultMaxGap))
	})
})

var _ = Describe("IOWorkload run ids", func() {
	// A framework with the naming state Setup() populates on a real run; no
	// cluster is involved in handing out names.
	f := &Framework{prefix: "e2e-unit", specCounters: map[any]int{}}

	unnamed := func() IOWorkloadOptions {
		return IOWorkloadOptions{
			NodeName:         testNode,
			DevicePath:       testDevicePath,
			DRBDResourceName: testDRBDName,
		}
	}

	It("gives every workload of a spec a run id of its own", func() {
		first, err := f.newIOWorkload(unnamed())
		Expect(err).NotTo(HaveOccurred())
		second, err := f.newIOWorkload(unnamed())
		Expect(err).NotTo(HaveOccurred())

		Expect(first.opts.RunID).To(MatchRegexp(runIDRe.String()))
		Expect(second.opts.RunID).To(MatchRegexp(runIDRe.String()))
		Expect(second.opts.RunID).NotTo(Equal(first.opts.RunID),
			"a shared default run id would make the second start adopt the first writer")
	})
})

var _ = Describe("IOWorkload device identity", func() {
	ctx := context.Background()

	It("starts once the device is proven to be this volume", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		w, stub := newTestIOWorkload(node)

		Expect(w.start(ctx)).To(Succeed())

		Expect(node.writerStarts).To(Equal(1))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(1))
		Expect(stub.countDisplaysWithPrefix("drbdsetup status")).To(Equal(1),
			"the expected minor must come from the kernel, not from the API object")
		Expect(stub.countDisplaysWithPrefix("readlink -f")).To(Equal(1))
	})

	It("refuses a device path that is not a DRBD device node, before writing anything", func() {
		node := newFakeIONode()
		node.canonical = "/dev/sda1\n"
		node.onSpawn = (*fakeIONode).startWriter
		w, stub := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("not a DRBD device node")))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(BeZero())
		Expect(node.writerStarts).To(BeZero())
		Expect(node.journal).To(BeEmpty(), "nothing may be written before identity is proven")
	})

	It("refuses a valid DRBD device that belongs to another resource", func() {
		node := newFakeIONode()
		node.canonical = "/dev/drbd2024\n" // another volume's device
		node.onSpawn = (*fakeIONode).startWriter
		w, stub := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("refusing to write to another volume's device")))
		Expect(err).To(MatchError(ContainSubstring("drbdsetup reports minor 1012")))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(BeZero())
		Expect(node.journal).To(BeEmpty())
	})

	It("fails when the writer's fstat catches a device swapped after validation", func() {
		node := newFakeIONode()
		// The path validated fine, but by the time the descriptor was opened it
		// pointed at another minor — only the fstat on the open fd sees this.
		node.onSpawn = (*fakeIONode).startAndFailIdentity
		w, _ := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("terminated before the first verified write")))
		Expect(err).To(MatchError(ContainSubstring("identity")))

		st, obsErr := w.observe(ctx)
		Expect(obsErr).NotTo(HaveOccurred())
		Expect(st.LastSequence).To(Equal(int64(-1)), "the writer must not have written a single record")
		Expect(st.Terminated.Failed).To(BeTrue())
	})
})

var _ = Describe("IOWorkload start idempotency", func() {
	ctx := context.Background()
	transport := errors.New("error dialing backend: connection reset by peer")

	It("adopts the running writer instead of spawning a second one", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		stub := &stubRunner{respond: node.respond}
		f := &Framework{nodeRun: stub}
		first, second := workloadOn(f), workloadOn(f)

		Expect(first.start(ctx)).To(Succeed())
		Expect(second.start(ctx)).To(Succeed())

		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(1))
		Expect(node.writerStarts).To(Equal(1))
		Expect(node.refusedSpawns).To(BeZero())
	})

	It("refuses to adopt a writer of the same run id that writes to another device", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		stub := &stubRunner{respond: node.respond}
		f := &Framework{nodeRun: stub}
		Expect(workloadOn(f).start(ctx)).To(Succeed())
		// The run id is now held by a writer of another volume's device — a
		// reused run id must not turn its I/O into evidence about ours.
		node.marker.Device = "/dev/sdsrv/rv-2-0"

		err := workloadOn(f).start(ctx)

		Expect(err).To(MatchError(ContainSubstring("refusing to adopt it")))
		Expect(err).To(MatchError(ContainSubstring("/dev/sdsrv/rv-2-0")))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(1))
		Expect(node.writerStarts).To(Equal(1))
	})

	It("clears the marker of a writer the node reboot killed, then starts a new one", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		stub := &stubRunner{respond: node.respond}
		f := &Framework{nodeRun: stub}
		Expect(workloadOn(f).start(ctx)).To(Succeed())

		// The node rebooted: the marker survives in /var/tmp, its writer does
		// not. Without clearing it the on-node program would refuse to start.
		node.bootID = "boot-b"

		Expect(workloadOn(f).start(ctx)).To(Succeed())

		Expect(stub.countDisplaysWithPrefix("io-workload clear-stale-marker")).To(Equal(1))
		Expect(node.writerStarts).To(Equal(2))
		Expect(node.refusedSpawns).To(BeZero())
		Expect(node.marker.BootID).To(Equal("boot-b"))
	})

	It("finds the writer when the spawn exec broke after forking it", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		node.spawnErr = transport
		w, stub := newTestIOWorkload(node)

		Expect(w.start(ctx)).To(Succeed())

		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(1),
			"the writer was already forked, so retrying would add a second one")
		Expect(node.writerStarts).To(Equal(1))

		// The lost writer is still reachable by run id, so cleanup ends it.
		Expect(w.cleanup(ctx)).To(Succeed())
		Expect(node.signals).To(ContainElement("TERM"))
		Expect(node.marker).To(BeNil())
	})

	It("retries a spawn whose transport error left no writer, then gives up", func() {
		node := newFakeIONode()
		node.spawnErr = transport
		w, stub := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("spawning the writer")))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(ioWorkloadSpawnAttempts))
		Expect(node.writerStarts).To(BeZero())
	})

	It("does not retry a spawn that ran and failed", func() {
		node := newFakeIONode()
		node.spawnExit = 127 // python3 missing on the node
		w, stub := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("exited with code 127")))
		Expect(stub.countDisplaysWithPrefix("io-workload spawn")).To(Equal(1))
	})

	It("fails when the spawn succeeded but no writer ever appeared", func() {
		node := newFakeIONode() // onSpawn does nothing
		w, _ := newTestIOWorkload(node)

		err := w.start(ctx)

		Expect(err).To(MatchError(ContainSubstring("waiting for the first verified write")))
	})
})

var _ = Describe("IOWorkload observation", func() {
	ctx := context.Background()

	startedWorkload := func(node *fakeIONode) (*IOWorkload, *stubRunner) {
		node.onSpawn = (*fakeIONode).startWriter
		w, stub := newTestIOWorkload(node)
		Expect(w.start(ctx)).To(Succeed())
		return w, stub
	}

	It("reports a healthy writer", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		st := mustObserve(ctx, w)

		Expect(st.Running).To(BeTrue())
		Expect(st.LastSequence).To(Equal(int64(0)))
		Expect(st.Stalled).To(BeFalse())
		Expect(st.Terminated).To(BeNil())
	})

	It("detects a stall when heartbeats stop arriving", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		st := mustObserve(ctx, w)

		Expect(st.Running).To(BeTrue(), "the process is alive — it is the data path that stopped")
		Expect(st.Stalled).To(BeTrue())
		Expect(st.Gap).To(BeNumerically(">", ioWorkloadDefaultMaxGap))
	})

	It("keeps a stall visible after writes resume (historical gap)", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		// The data path freezes for longer than tolerated, then recovers: the
		// writer never dies, and by the time anyone probes it is writing
		// again. The gap between the last pre-freeze and the first post-freeze
		// beat is the only remaining evidence.
		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		node.beat()
		node.beat()
		st := mustObserve(ctx, w)

		Expect(st.Stalled).To(BeFalse(), "the last write is fresh — the stall is over")
		Expect(st.GapExceeded).To(BeTrue())
		Expect(st.MaxObservedGap).To(BeNumerically(">", ioWorkloadDefaultMaxGap))
	})

	It("fails a progress wait on a stall that already ended", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		node.beat()
		_, err := w.awaitProgress(ctx, 1)

		Expect(err).To(MatchError(ContainSubstring("the writer stalled")))
		// The verdict names the policy it was measured against, so a reader
		// does not have to know the defaults to judge the number.
		Expect(err).To(MatchError(ContainSubstring("no gap longer than " + ioWorkloadDefaultMaxGap.String())))
	})

	It("does not call a terminated writer stalled", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		node.failIO("sequence 1: [Errno 5] Input/output error")
		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		st := mustObserve(ctx, w)

		Expect(st.Stalled).To(BeFalse())
		Expect(st.Running).To(BeFalse())
		Expect(st.Terminated.Failed).To(BeTrue())
		Expect(st.Terminated.Message).To(ContainSubstring("Input/output error"))
	})

	It("fails a progress wait when the writer dies of an I/O error", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)

		node.failIO("sequence 1: [Errno 5] Input/output error")
		_, err := w.awaitProgress(ctx, 3)

		Expect(err).To(MatchError(ContainSubstring("the writer failed")))
		Expect(err).To(MatchError(ContainSubstring("Input/output error")))
	})

	It("waits for further verified writes", func() {
		node := newFakeIONode()
		node.beatOnPeek = true
		w, _ := startedWorkload(node)

		before := mustObserve(ctx, w)
		st, err := w.awaitProgress(ctx, 3)

		Expect(err).NotTo(HaveOccurred())
		Expect(st.LastSequence).To(BeNumerically(">=", before.LastSequence+3))
	})
})

var _ = Describe("IOWorkload signalling", func() {
	ctx := context.Background()

	startedWorkload := func(node *fakeIONode) *IOWorkload {
		node.onSpawn = (*fakeIONode).startWriter
		w, _ := newTestIOWorkload(node)
		Expect(w.start(ctx)).To(Succeed())
		return w
	}

	It("stops the writer it started", func() {
		node := newFakeIONode()
		w := startedWorkload(node)

		Expect(w.stop(ctx)).To(Succeed())

		Expect(node.signals).To(Equal([]string{"TERM"}))
		Expect(node.marker).To(BeNil())
	})

	It("is a no-op when the writer is already gone", func() {
		node := newFakeIONode()
		w := startedWorkload(node)
		Expect(w.stop(ctx)).To(Succeed())

		Expect(w.stop(ctx)).To(Succeed())

		Expect(node.signals).To(Equal([]string{"TERM"}))
	})

	It("does not signal after the node rebooted, even though the pid is still there", func() {
		node := newFakeIONode()
		w := startedWorkload(node)
		node.bootID = "boot-b" // the node rebooted; this pid belongs to someone else

		Expect(w.stop(ctx)).To(Succeed())

		Expect(node.signals).To(BeEmpty())
		Expect(mustObserve(ctx, w).Note).To(ContainSubstring("node rebooted"))
	})

	It("does not signal a recycled pid", func() {
		node := newFakeIONode()
		w := startedWorkload(node)
		node.procStat = procStatLine(testWriterPID, "999999") // same pid, another process

		Expect(w.stop(ctx)).To(Succeed())

		Expect(node.signals).To(BeEmpty())
		Expect(mustObserve(ctx, w).Note).To(ContainSubstring("pid reused"))
	})
})

var _ = Describe("IOWorkload cleanup", func() {
	ctx := context.Background()

	startedWorkload := func(node *fakeIONode) (*IOWorkload, *stubRunner) {
		node.onSpawn = (*fakeIONode).startWriter
		w, stub := newTestIOWorkload(node)
		Expect(w.start(ctx)).To(Succeed())
		return w, stub
	}

	It("stops the writer, checks the last record, and only then removes the journal", func() {
		node := newFakeIONode()
		w, stub := startedWorkload(node)

		Expect(w.cleanup(ctx)).To(Succeed())

		signalAt := stub.indexOfDisplayPrefix("io-workload signal")
		purgeAt := stub.indexOfDisplayPrefix("io-workload purge")
		Expect(signalAt).To(BeNumerically(">=", 0))
		Expect(purgeAt).To(BeNumerically(">", signalAt), "the writer must be stopped before its files are removed")

		lastProbeAt := -1
		for i, display := range stub.displays() {
			if strings.HasPrefix(display, "io-workload probe") {
				lastProbeAt = i
			}
		}
		Expect(lastProbeAt).To(BeNumerically(">", signalAt))
		Expect(lastProbeAt).To(BeNumerically("<", purgeAt),
			"the last record must be read while the journal still exists")
		Expect(node.purged).To(BeTrue())
	})

	It("reports a writer that died of an I/O error", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)
		node.failIO("sequence 7: [Errno 5] Input/output error")

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("the writer failed")))
		Expect(err).To(MatchError(ContainSubstring("Input/output error")))
		Expect(node.purged).To(BeTrue(), "the node must be left clean even when the workload failed")
	})

	It("kills a writer that ignored SIGTERM", func() {
		node := newFakeIONode()
		node.ignoreTerm = true
		w, _ := startedWorkload(node)

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("waiting for the writer to stop")))
		Expect(node.signals).To(ContainElement("KILL"))
		Expect(node.purged).To(BeTrue())
	})

	It("is idempotent", func() {
		node := newFakeIONode()
		w, stub := startedWorkload(node)
		Expect(w.cleanup(ctx)).To(Succeed())
		callsAfterFirst := len(stub.calls)

		Expect(w.cleanup(ctx)).To(Succeed())

		Expect(stub.calls).To(HaveLen(callsAfterFirst), "a second cleanup must not touch the node again")
	})

	It("is idempotent across two handles that share one writer", func() {
		node := newFakeIONode()
		node.onSpawn = (*fakeIONode).startWriter
		stub := &stubRunner{respond: node.respond}
		f := &Framework{nodeRun: stub}
		first, second := workloadOn(f), workloadOn(f)
		Expect(first.start(ctx)).To(Succeed())
		Expect(second.start(ctx)).To(Succeed()) // adopts the same writer
		Expect(first.cleanup(ctx)).To(Succeed())

		// The journal went with the first cleanup, so the second handle has
		// nothing left to verify — and must not report that as a failure.
		Expect(second.cleanup(ctx)).To(Succeed())
		Expect(node.purged).To(BeTrue())
	})

	It("still reports a writer that wrote nothing while its journal is there", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)
		// The writer is gone and its journal holds the start record alone: an
		// empty run id is "already cleaned up", this one is "no evidence".
		node.marker = nil
		node.procStat = ""
		node.journal = []string{fmt.Sprintf(
			"start %d %d %s 147:%d 1048576", node.nowMS, testWriterPID, testDevicePath, testMinor)}

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("completed no verified write")))
	})

	It("fails when the run contained a stall, even after a clean stop", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)
		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		node.beat()

		err := w.cleanup(ctx)

		Expect(err).To(MatchError(ContainSubstring("the writer stalled during the run")))
		Expect(node.purged).To(BeTrue(), "the verdict must not leave the writer's files behind")
	})

	It("is safe when the writer already exited on its own", func() {
		node := newFakeIONode()
		w, _ := startedWorkload(node)
		node.marker = nil
		node.procStat = ""
		node.journal = append(node.journal, fmt.Sprintf("stopped %d %d", node.sequence, node.nowMS))

		Expect(w.cleanup(ctx)).To(Succeed())

		Expect(node.signals).To(BeEmpty())
		Expect(node.purged).To(BeTrue())
	})
})

var _ = Describe("IOWorkload declared freeze", func() {
	ctx := context.Background()

	// declaredBound is comfortably above the default heartbeat gap, so a freeze
	// can be built both inside and outside it.
	const declaredBound = 10 * time.Minute

	startedWorkload := func(node *fakeIONode) *IOWorkload {
		node.onSpawn = (*fakeIONode).startWriter
		w, _ := newTestIOWorkload(node)
		Expect(w.start(ctx)).To(Succeed())
		return w
	}

	// freezeFor stops the writer for d and then lets it write again, which is
	// what leaves a historical gap of d in the journal.
	freezeFor := func(node *fakeIONode, d time.Duration) {
		node.nowMS += d.Milliseconds()
		node.beat()
		node.beat()
	}

	Describe("declaring it", func() {
		It("records the bound", func() {
			w := startedWorkload(newFakeIONode())

			Expect(w.declareFreeze(declaredBound)).To(Succeed())

			Expect(w.freeze).To(Equal(ioFreezeAllowance{declared: true, max: declaredBound}))
		})

		DescribeTable("refuses a declaration that would amount to switching the checks off",
			func(first, second time.Duration, wantMsg string) {
				w := startedWorkload(newFakeIONode())
				if first > 0 {
					Expect(w.declareFreeze(first)).To(Succeed())
				}

				err := w.declareFreeze(second)

				Expect(err).To(MatchError(ContainSubstring(wantMsg)))
			},
			Entry("no bound at all", time.Duration(0), time.Duration(0), "finite positive upper bound"),
			Entry("a negative bound", time.Duration(0), -time.Minute, "finite positive upper bound"),
			Entry("a bound below the heartbeat gap", time.Duration(0), ioWorkloadDefaultMaxGap/2, "does not exceed MaxHeartbeatGap"),
			Entry("a bound equal to the heartbeat gap", time.Duration(0), ioWorkloadDefaultMaxGap, "does not exceed MaxHeartbeatGap"),
			Entry("a second declaration", declaredBound, declaredBound, "already declared"),
		)
	})

	Describe("a freeze within the declared bound", func() {
		It("does not fail a progress wait", func() {
			node := newFakeIONode()
			w := startedWorkload(node)
			Expect(w.declareFreeze(declaredBound)).To(Succeed())

			freezeFor(node, ioWorkloadDefaultMaxGap+time.Minute)
			node.beatOnPeek = true // the writer is going again after the freeze
			st, err := w.awaitProgress(ctx, 1)

			Expect(err).NotTo(HaveOccurred())
			Expect(st.GapExceeded).To(BeFalse())
			// The stall is still MEASURED and reported — the declaration only
			// changes the verdict, never the evidence.
			Expect(st.Stalls).To(HaveLen(1))
			Expect(st.MaxObservedGap).To(BeNumerically(">", ioWorkloadDefaultMaxGap))
		})

		It("does not fail the final verification", func() {
			node := newFakeIONode()
			w := startedWorkload(node)
			Expect(w.declareFreeze(declaredBound)).To(Succeed())
			freezeFor(node, ioWorkloadDefaultMaxGap+time.Minute)

			// Cleanup is where an undeclared freeze fails a spec that had long
			// since passed, so it is the half that matters most.
			Expect(w.cleanup(ctx)).To(Succeed())
		})
	})

	Describe("a freeze beyond the declared bound", func() {
		It("fails a progress wait", func() {
			node := newFakeIONode()
			w := startedWorkload(node)
			Expect(w.declareFreeze(declaredBound)).To(Succeed())

			freezeFor(node, declaredBound+time.Minute)
			_, err := w.awaitProgress(ctx, 1)

			Expect(err).To(MatchError(ContainSubstring("the writer stalled")))
			Expect(err).To(MatchError(ContainSubstring("ONE declared freeze of up to " + declaredBound.String())))
		})

		It("fails the final verification", func() {
			node := newFakeIONode()
			w := startedWorkload(node)
			Expect(w.declareFreeze(declaredBound)).To(Succeed())
			freezeFor(node, declaredBound+time.Minute)

			Expect(w.cleanup(ctx)).NotTo(Succeed())
		})

		It("fails on a SECOND freeze even when both are inside the bound", func() {
			// One freeze was declared, not a series: a volume that keeps
			// freezing is a different story from the one the spec is telling.
			node := newFakeIONode()
			w := startedWorkload(node)
			Expect(w.declareFreeze(declaredBound)).To(Succeed())

			freezeFor(node, ioWorkloadDefaultMaxGap+time.Minute)
			freezeFor(node, ioWorkloadDefaultMaxGap+time.Minute)
			st := mustObserve(ctx, w)

			Expect(st.Stalls).To(HaveLen(2))
			Expect(st.GapExceeded).To(BeTrue())
		})
	})

	Describe("without a declaration", func() {
		It("fails a progress wait exactly as before", func() {
			// The relaxation must not leak into the rest of the suite: a
			// workload nobody declared anything for keeps the plain rule.
			node := newFakeIONode()
			w := startedWorkload(node)

			freezeFor(node, ioWorkloadDefaultMaxGap+time.Second)
			_, err := w.awaitProgress(ctx, 1)

			Expect(err).To(MatchError(ContainSubstring("the writer stalled")))
			Expect(err).To(MatchError(ContainSubstring("no gap longer than " + ioWorkloadDefaultMaxGap.String())))
			Expect(err).NotTo(MatchError(ContainSubstring("declared freeze")))
		})

		It("fails the final verification exactly as before", func() {
			node := newFakeIONode()
			w := startedWorkload(node)
			freezeFor(node, ioWorkloadDefaultMaxGap+time.Second)

			Expect(w.cleanup(ctx)).NotTo(Succeed())
		})
	})

	It("never widens the live stall observation", func() {
		// Stalled is what a spec waits on to learn that the freeze has begun,
		// so it stays a plain measurement against MaxHeartbeatGap.
		node := newFakeIONode()
		w := startedWorkload(node)
		Expect(w.declareFreeze(declaredBound)).To(Succeed())

		node.nowMS += (ioWorkloadDefaultMaxGap + time.Second).Milliseconds()
		st := mustObserve(ctx, w)

		Expect(st.Stalled).To(BeTrue())
		Expect(st.GapExceeded).To(BeFalse())
	})
})

// mustObserve observes the workload and fails the spec on a probe error.
func mustObserve(ctx context.Context, w *IOWorkload) IOWorkloadStatus {
	st, err := w.observe(ctx)
	Expect(err).NotTo(HaveOccurred())
	return st
}
