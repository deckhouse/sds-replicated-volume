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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The commands below are executed on a node, where nothing checks them before
// they run: a quoting mistake surfaces as a workload that never starts, hours
// into a cluster run. These specs check them here instead.
var _ = Describe("IOWorkload node commands", func() {
	w := &IOWorkload{opts: IOWorkloadOptions{
		NodeName:         testNode,
		DevicePath:       testDevicePath,
		DRBDResourceName: testDRBDName,
		RunID:            testRunID,
		Slots:            ioWorkloadDefaultSlots,
		SlotSize:         ioWorkloadDefaultSlotSize,
		Interval:         ioWorkloadDefaultInterval,
	}}

	DescribeTable("are valid shell",
		func(cmd []string) {
			Expect(cmd[:2]).To(Equal([]string{"sh", "-c"}))

			script := filepath.Join(GinkgoT().TempDir(), "script.sh")
			Expect(os.WriteFile(script, []byte(cmd[2]), 0o600)).To(Succeed())

			out, err := exec.Command("sh", "-n", script).CombinedOutput()

			Expect(err).NotTo(HaveOccurred(), "sh -n: %s", out)
		},
		Entry("spawn", w.spawnCommand()),
		Entry("probe", w.probeCommand(ioWorkloadJournalTail)),
		Entry("signal", w.signalCommand("TERM", testWriterStart)),
		Entry("clear stale marker", w.clearMarkerCommand()),
		Entry("purge", w.purgeCommand()),
	)

	It("prints the spawned marker instead of commenting it out", func() {
		// An unquoted word starting with '#' is a comment in sh, so this line
		// silently prints nothing when the marker is not quoted — and the whole
		// spawn is then reported as a writer that failed to announce itself.
		line := lineContaining(w.spawnCommand()[2], spawnedMarker)

		out, err := exec.Command("sh", "-c", line).Output()

		Expect(err).NotTo(HaveOccurred())
		Expect(string(out)).To(Equal(spawnedMarker + "\n"))
	})

	It("hands the writer its identity, its ring and its files", func() {
		script := w.spawnCommand()[2]

		Expect(script).To(ContainSubstring("setsid python3 "),
			"the writer must outlive the exec session that spawned it")
		Expect(script).To(ContainSubstring(fmt.Sprintf("%s %s %s %d %s %s %d %d %d",
			w.programPath(), w.opts.RunID, w.opts.DevicePath, w.minor,
			w.journalPath(), w.markerPath(),
			w.opts.Slots, w.opts.SlotSize, w.opts.Interval.Milliseconds())))
	})

	It("keeps the node's clock parsable on a shell without date +%N", func() {
		// BusyBox prints the format verbatim instead of nanoseconds, which would
		// make every observation fail on an unparsable timestamp.
		script := w.probeCommand(1)[2]

		Expect(script).To(ContainSubstring(`case "$n" in ""|*[!0-9]*)`))
	})
})

var _ = Describe("IOWorkload writer program", func() {
	It("is valid Python", func() {
		python, err := exec.LookPath("python3")
		if err != nil {
			Skip("python3 is not installed here; the writer program cannot be syntax-checked")
		}

		path := filepath.Join(GinkgoT().TempDir(), "writer.py")
		Expect(os.WriteFile(path, []byte(ioWorkloadProgram), 0o600)).To(Succeed())

		out, err := exec.Command(python, "-m", "py_compile", path).CombinedOutput()

		Expect(err).NotTo(HaveOccurred(), "py_compile: %s", out)
	})

	It("verifies identity on the descriptor it writes through", func() {
		// The order matters more than the calls: fstat must sit between the
		// single open and the first write, or the device could be swapped in
		// between.
		open := strings.Index(ioWorkloadProgram, "fd = os.open(DEVICE")
		fstat := strings.Index(ioWorkloadProgram, "info = os.fstat(fd)")
		write := strings.Index(ioWorkloadProgram, "os.pwrite(fd")

		Expect(open).To(BeNumerically(">", 0))
		Expect(fstat).To(BeNumerically(">", open))
		Expect(write).To(BeNumerically(">", fstat))
		Expect(strings.Count(ioWorkloadProgram, "os.open(DEVICE")).To(Equal(1),
			"the device must be opened exactly once")
	})

	It("publishes its marker before it touches the device", func() {
		marker := strings.Index(ioWorkloadProgram, "if not publish_marker():")
		open := strings.Index(ioWorkloadProgram, "fd = open_verified()")

		Expect(marker).To(BeNumerically(">", 0))
		Expect(open).To(BeNumerically(">", marker))
		Expect(ioWorkloadProgram).To(ContainSubstring("os.link(tmp, MARKER)"),
			"the marker must be published atomically, or two writers could race")
	})

	It("blames a short pwrite or pread on the syscall, not on the read-back", func() {
		// A short syscall does not raise: it returns the byte count it moved.
		// Unchecked, it first surfaces in the content compare below — as "the
		// device gave back other data" instead of "the device moved fewer
		// bytes than asked", which sends the investigation the wrong way.
		write := strings.Index(ioWorkloadProgram, "written = os.pwrite(fd")
		readBack := strings.Index(ioWorkloadProgram, "back = os.pread(fd")
		shortWrite := strings.Index(ioWorkloadProgram, `die("short write:`)
		shortRead := strings.Index(ioWorkloadProgram, `die("short read:`)
		compare := strings.Index(ioWorkloadProgram, "if back != buf:")

		Expect(write).To(BeNumerically(">", 0),
			"the pwrite result must be captured, or a short write cannot be seen at all")
		Expect(ioWorkloadProgram).To(ContainSubstring("if written != len(buf):"),
			"a write is short unless it moved the whole record")
		Expect(ioWorkloadProgram).To(ContainSubstring("if len(back) != SLOT_SIZE:"),
			"a read is short unless it returned the whole slot")
		Expect(shortWrite).To(BeNumerically(">", write))
		Expect(shortWrite).To(BeNumerically("<", compare),
			"a short write must be diagnosed before the content compare")
		Expect(shortRead).To(BeNumerically(">", readBack),
			"the read guard must sit after the pread, or `back` is not bound yet")
		Expect(shortRead).To(BeNumerically("<", compare),
			"a short read must be diagnosed before the content compare")
	})

	It("names short I/O and a corrupted read-back as different failures", func() {
		// die() puts the message into the journal and stderr, and that message
		// is the whole diagnosis a run leaves behind.
		Expect(lineContaining(ioWorkloadProgram, `die("short write:`)).
			To(ContainSubstring("got %d bytes, want %d"),
				"a short write must name both counts")
		Expect(lineContaining(ioWorkloadProgram, `die("short read:`)).
			To(ContainSubstring("got %d bytes, want %d"),
				"a short read must name both counts")
		Expect(lineContaining(ioWorkloadProgram, `die("readback:`)).
			NotTo(ContainSubstring("short"),
				"the corruption message must not read like a short-I/O one")
	})

	It("journals a heartbeat only after the write was read back and compared", func() {
		sync := strings.Index(ioWorkloadProgram, "os.fdatasync(fd)")
		readBack := strings.Index(ioWorkloadProgram, "back = os.pread(fd")
		compare := strings.Index(ioWorkloadProgram, "if back != buf:")
		beat := strings.Index(ioWorkloadProgram, `journal("ok %d %d %d %08x"`)

		Expect(sync).To(BeNumerically(">", 0))
		Expect(readBack).To(BeNumerically(">", sync))
		Expect(compare).To(BeNumerically(">", readBack))
		Expect(beat).To(BeNumerically(">", compare))
	})
})

// lineContaining returns the last line of script that mentions sub.
func lineContaining(script, sub string) string {
	found := ""
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, sub) {
			found = line
		}
	}
	Expect(found).NotTo(BeEmpty(), "no line of the script mentions %q", sub)
	return found
}
