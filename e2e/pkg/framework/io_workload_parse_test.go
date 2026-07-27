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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// procStatLine renders a /proc/<pid>/stat line whose field 22 (starttime) is
// startTime. The comm field deliberately contains a space and parentheses,
// which is exactly what naive field splitting gets wrong.
func procStatLine(pid int, startTime string) string {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[19] = startTime
	return fmt.Sprintf("%d (python3 (io)) %s", pid, strings.Join(fields, " "))
}

var _ = Describe("parseDRBDDevicePath", func() {
	It("accepts the canonical DRBD device node and returns its minor", func() {
		minor, err := parseDRBDDevicePath("/dev/drbd1012\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(1012))
	})

	DescribeTable("refuses anything that is not a DRBD device node",
		func(path string) {
			_, err := parseDRBDDevicePath(path)
			Expect(err).To(MatchError(ContainSubstring("not a DRBD device node")))
		},
		Entry("empty", ""),
		Entry("regular file", "/tmp/not-a-device"),
		Entry("another driver", "/dev/sda1"),
		Entry("LVM device", "/dev/vg0/lv0"),
		Entry("unresolved symlink dir", "/dev/sdsrv/rv-1-0"),
		Entry("suffixed", "/dev/drbd10p1"),
		Entry("relative", "drbd10"),
	)
})

var _ = Describe("parseDRBDMinorFromStatus", func() {
	const statusJSON = `[
      {"name":"sdsrv-rv-1-0","node-id":0,"role":"Primary",
       "devices":[{"volume":0,"minor":1012,"disk-state":"UpToDate"}]},
      {"name":"sdsrv-other","node-id":1,"role":"Secondary",
       "devices":[{"volume":0,"minor":2024,"disk-state":"UpToDate"}]}
    ]`

	It("returns the minor of the requested resource", func() {
		minor, err := parseDRBDMinorFromStatus(statusJSON, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(1012))
	})

	It("does not confuse resources", func() {
		minor, err := parseDRBDMinorFromStatus(statusJSON, "sdsrv-other")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(2024))
	})

	It("fails when the resource is absent", func() {
		_, err := parseDRBDMinorFromStatus(statusJSON, "sdsrv-missing")
		Expect(err).To(MatchError(ContainSubstring(`"sdsrv-missing" not found`)))
	})

	It("fails on a multi-volume resource instead of guessing", func() {
		_, err := parseDRBDMinorFromStatus(
			`[{"name":"r","devices":[{"volume":0,"minor":1},{"volume":1,"minor":2}]}]`, "r")
		Expect(err).To(MatchError(ContainSubstring("reports 2 devices")))
	})

	It("fails on non-JSON output", func() {
		_, err := parseDRBDMinorFromStatus("drbdsetup: no resources defined!", "r")
		Expect(err).To(MatchError(ContainSubstring("parsing drbdsetup status")))
	})
})

var _ = Describe("parseProcStartTime", func() {
	It("reads field 22 past a comm containing spaces and parentheses", func() {
		startTime, err := parseProcStartTime(procStatLine(4242, "918273"))
		Expect(err).NotTo(HaveOccurred())
		Expect(startTime).To(Equal("918273"))
	})

	It("fails on a truncated stat line", func() {
		_, err := parseProcStartTime("4242 (python3) S 1 2 3")
		Expect(err).To(MatchError(ContainSubstring("only 4 fields after comm")))
	})

	It("fails when there is no comm field", func() {
		_, err := parseProcStartTime("garbage")
		Expect(err).To(MatchError(ContainSubstring("no comm field")))
	})
})

var _ = Describe("parseIOMarker", func() {
	It("decodes a marker", func() {
		m, err := parseIOMarker(`{"runID":"run-1","pid":4242,"procStartTime":"918273","bootID":"boot-a","device":"/dev/sdsrv/x"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.RunID).To(Equal("run-1"))
		Expect(m.PID).To(Equal(4242))
		Expect(m.ProcStartTime).To(Equal("918273"))
		Expect(m.BootID).To(Equal("boot-a"))
	})

	It("treats an empty file as no marker", func() {
		m, err := parseIOMarker("  \n")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())
	})

	It("fails on a marker without a pid", func() {
		_, err := parseIOMarker(`{"runID":"run-1"}`)
		Expect(err).To(MatchError(ContainSubstring("lacks runID or pid")))
	})

	It("fails on malformed JSON", func() {
		_, err := parseIOMarker(`{"runID":`)
		Expect(err).To(MatchError(ContainSubstring("parsing workload marker")))
	})
})

var _ = Describe("matchIOMarker", func() {
	marker := &ioMarker{RunID: "run-1", PID: 4242, ProcStartTime: "918273", BootID: "boot-a"}

	It("matches a live process with the recorded start time", func() {
		ok, why := matchIOMarker(marker, "run-1", "boot-a", procStatLine(4242, "918273"))
		Expect(ok).To(BeTrue())
		Expect(why).To(BeEmpty())
	})

	It("does not match when there is no marker", func() {
		ok, why := matchIOMarker(nil, "run-1", "boot-a", "")
		Expect(ok).To(BeFalse())
		Expect(why).To(ContainSubstring("no marker file"))
	})

	It("does not match a marker of another run", func() {
		ok, why := matchIOMarker(marker, "run-2", "boot-a", procStatLine(4242, "918273"))
		Expect(ok).To(BeFalse())
		Expect(why).To(ContainSubstring(`belongs to run "run-1"`))
	})

	It("does not match after a reboot, even with the same pid and start time", func() {
		ok, why := matchIOMarker(marker, "run-1", "boot-b", procStatLine(4242, "918273"))
		Expect(ok).To(BeFalse())
		Expect(why).To(ContainSubstring("node rebooted"))
	})

	It("does not match when the process is gone", func() {
		ok, why := matchIOMarker(marker, "run-1", "boot-a", "")
		Expect(ok).To(BeFalse())
		Expect(why).To(ContainSubstring("process 4242 is gone"))
	})

	It("does not match a recycled pid", func() {
		ok, why := matchIOMarker(marker, "run-1", "boot-a", procStatLine(4242, "999999"))
		Expect(ok).To(BeFalse())
		Expect(why).To(ContainSubstring("pid reused"))
	})
})

var _ = Describe("parseIOJournal", func() {
	It("parses start, heartbeat and stop records", func() {
		j, err := parseIOJournal(strings.Join([]string{
			"start 1750000000000 4242 /dev/sdsrv/x 147:1012 1048576",
			"ok 0 0 1750000000100 a1b2c3d4",
			"ok 1 1 1750000000300 b2c3d4e5",
			"stopped 2 1750000000500",
			"",
		}, "\n"))

		Expect(err).NotTo(HaveOccurred())
		Expect(j.Started).To(BeTrue())
		Expect(j.Beats).To(HaveLen(2))
		Expect(j.last().Sequence).To(Equal(int64(1)))
		Expect(j.last().Slot).To(Equal(1))
		Expect(j.last().Checksum).To(Equal("b2c3d4e5"))
		Expect(j.Termination).NotTo(BeNil())
		Expect(j.Termination.Failed).To(BeFalse())
	})

	It("parses a tail that starts mid-stream", func() {
		j, err := parseIOJournal("ok 512 0 1750000000100 a1b2c3d4\nok 513 1 1750000000300 b2c3d4e5\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(j.Started).To(BeFalse())
		Expect(j.last().Sequence).To(Equal(int64(513)))
	})

	It("reports a failure record", func() {
		j, err := parseIOJournal("ok 0 0 1750000000100 a1b2c3d4\nfail 1750000000200 io: sequence 1: [Errno 5] Input/output error\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(j.Termination).NotTo(BeNil())
		Expect(j.Termination.Failed).To(BeTrue())
		Expect(j.Termination.Message).To(ContainSubstring("Input/output error"))
	})

	It("rejects a non-monotonic sequence", func() {
		_, err := parseIOJournal("ok 5 0 1750000000100 a1b2c3d4\nok 4 1 1750000000300 b2c3d4e5\n")
		Expect(err).To(MatchError(ContainSubstring("not monotonic")))
	})

	It("rejects a repeated sequence", func() {
		_, err := parseIOJournal("ok 5 0 1750000000100 a1b2c3d4\nok 5 1 1750000000300 b2c3d4e5\n")
		Expect(err).To(MatchError(ContainSubstring("not monotonic")))
	})

	It("ignores a last line the writer is still appending", func() {
		j, err := parseIOJournal("ok 0 0 1750000000100 a1b2c3d4\nok 1 1 17500")
		Expect(err).NotTo(HaveOccurred())
		Expect(j.Beats).To(HaveLen(1))
	})

	It("rejects a malformed record that is not the last one", func() {
		_, err := parseIOJournal("ok 0 0 nonsense a1b2c3d4\nok 1 1 1750000000300 b2c3d4e5\n")
		Expect(err).To(MatchError(ContainSubstring("malformed ok record")))
	})

	It("rejects an unknown record", func() {
		_, err := parseIOJournal("who-knows 1 2 3\nok 1 1 1750000000300 b2c3d4e5\n")
		Expect(err).To(MatchError(ContainSubstring("unknown journal record")))
	})

	It("accepts an empty journal", func() {
		j, err := parseIOJournal("")
		Expect(err).NotTo(HaveOccurred())
		Expect(j.last()).To(BeNil())
	})
})

var _ = Describe("parseIOProbe", func() {
	It("parses the whole envelope", func() {
		out := strings.Join([]string{
			"#now 1750000001000",
			"#boot boot-a",
			`#marker {"runID":"run-1","pid":4242,"procStartTime":"918273","bootID":"boot-a"}`,
			"#proc " + procStatLine(4242, "918273"),
			"#journal",
			"ok 7 7 1750000000900 a1b2c3d4",
			"",
		}, "\n")

		p, err := parseIOProbe(out)

		Expect(err).NotTo(HaveOccurred())
		Expect(p.Now.UnixMilli()).To(Equal(int64(1750000001000)))
		Expect(p.BootID).To(Equal("boot-a"))
		Expect(p.Marker).NotTo(BeNil())
		Expect(p.Marker.PID).To(Equal(4242))
		Expect(p.ProcStat).To(ContainSubstring("(python3 (io))"))
		Expect(p.Journal.last().Sequence).To(Equal(int64(7)))
	})

	It("parses an envelope with neither marker nor process nor journal", func() {
		p, err := parseIOProbe("#now 1750000001000\n#boot boot-a\n#marker \n#proc \n#journal\n")

		Expect(err).NotTo(HaveOccurred())
		Expect(p.Marker).To(BeNil())
		Expect(p.ProcStat).To(BeEmpty())
		Expect(p.Journal.last()).To(BeNil())
	})

	It("accepts a node whose date(1) prints seconds", func() {
		p, err := parseIOProbe("#now 1750000001\n#boot boot-a\n#journal\n")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Now.Unix()).To(Equal(int64(1750000001)))
	})

	It("fails when the node clock is missing", func() {
		_, err := parseIOProbe("#boot boot-a\n#journal\n")
		Expect(err).To(MatchError(ContainSubstring("no #now line")))
	})
})
