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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The writer program and the probe commands run inside a pod, where nothing
// checks them before they run: a quoting mistake surfaces as a workload that
// never beats, hours into a cluster run. These specs check them here — first as
// text, then by actually running the writer against a local directory, which is
// the only way to prove that the journal it produces is the journal
// parseIOJournal reads.
var _ = Describe("PodIOWorkload pod commands", func() {
	w, err := (&Framework{}).newPodIOWorkload(nil, PodIOWorkloadOptions{
		Namespace:        testPodIONS,
		StorageClassName: testPodIOSC,
		Name:             testPodIOName,
	})
	Expect(err).NotTo(HaveOccurred())

	DescribeTable("are valid shell",
		func(cmd []string) {
			Expect(cmd[:2]).To(Equal([]string{"sh", "-c"}))

			Expect(shellSyntaxError(cmd[2])).To(Succeed())
		},
		Entry("writer program", []string{"sh", "-c", w.program()}),
		Entry("probe", w.probeCommand(ioWorkloadJournalTail)),
		Entry("checksum", w.checksumCommand()),
		Entry("stop", w.stopCommand()),
	)

	It("binds every value the program reads", func() {
		header, _, found := strings.Cut(w.program(), podIOWorkloadProgram)

		Expect(found).To(BeTrue(), "the program must be the constant plus a header of assignments")
		Expect(header).To(ContainSubstring("dir=" + podIODir))
		Expect(header).To(ContainSubstring("interval=1"))
		Expect(header).To(ContainSubstring(fmt.Sprintf("records=%d", podIODefaultDataKiB*podIORecordsPerKiB)))
		Expect(header).To(ContainSubstring("record='" + podIODataRecord + "'"))
	})

	It("floors the beat interval at a whole second", func() {
		fast, err := (&Framework{}).newPodIOWorkload(nil, PodIOWorkloadOptions{
			Namespace:        testPodIONS,
			StorageClassName: testPodIOSC,
			Name:             testPodIOName,
			// BusyBox has no fractional sleep and no sub-second date format, so a
			// faster beat rate would neither sleep nor timestamp.
			Interval: 100 * time.Millisecond,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(fast.program()).To(ContainSubstring("interval=1"))
	})

	It("takes the pod's own clock and keeps it parsable without date +%N", func() {
		script := w.probeCommand(1)[2]

		Expect(script).To(ContainSubstring(`"$(date +%s)000"`),
			"%3N is a GNU extension BusyBox prints verbatim, which would make every probe unparsable")
		Expect(script).NotTo(ContainSubstring("%3N"))
	})

	It("reads the whole journal when asked to", func() {
		Expect(w.probeCommand(ioWorkloadJournalFull)[2]).
			To(ContainSubstring(fmt.Sprintf("tail -n %d %s", ioWorkloadJournalFull, w.journalPath())))
	})

	It("never exits on a failure, so the journal stays readable", func() {
		// die() journals the reason and then idles: the journal and the data file
		// are read by exec'ing into this very container, so a container that exits
		// (or crash-loops) takes the only way to read its own evidence with it.
		die := lineContaining(podIOWorkloadProgram, "die() {")
		Expect(die).NotTo(BeEmpty())
		Expect(podIOWorkloadProgram).To(ContainSubstring("printf 'pod-io-workload: %s\\n' \"$1\" >&2\n    idle"))
		Expect(podIOWorkloadProgram).NotTo(ContainSubstring("exit 1\n}"),
			"a failure must be journalled and idled, not exited")
	})

	It("fsyncs one file rather than every filesystem of the node", func() {
		// sync(2) without an argument flushes EVERY filesystem on the node: one
		// frozen volume would then stall the beats of every other workload on that
		// node, and the freeze would be reported against healthy volumes.
		Expect(podIOWorkloadProgram).To(ContainSubstring(`sync -d "$1"`))
		Expect(podIOWorkloadProgram).To(ContainSubstring(`sync_mode=file`))
		Expect(podIOWorkloadProgram).To(ContainSubstring(`sync -d "$beat" 2>/dev/null || sync_mode=all`),
			"a shell whose sync takes no file argument must still work")
	})

	It("never answers a failed per-file flush with the global sync", func() {
		// The global sync is the fallback for a shell whose sync takes no file
		// argument, chosen once at startup. Reaching for it when a per-file flush
		// FAILED would swallow the failure instead: sync(2) reports an error to
		// nobody, ever, and the read-back that follows is served from the page
		// cache, so every beat after an EIO on flush would still be green.
		Expect(podIOWorkloadProgram).To(ContainSubstring(strings.Join([]string{
			`sync_file() {`,
			`    if [ "$sync_mode" = file ]; then`,
			`        sync -d "$1"`,
			`    else`,
			`        sync`,
			`    fi`,
			`}`,
		}, "\n")))
	})

	It("publishes a beat only after the write was fsynced, read back and compared", func() {
		write := strings.Index(podIOWorkloadProgram, `printf '%s\n' "$payload" >"$beat"`)
		sync := strings.Index(podIOWorkloadProgram, `sync_file "$beat" || die "io: flushing the beat file`)
		readBack := strings.Index(podIOWorkloadProgram, `back=$(cat "$beat")`)
		compare := strings.Index(podIOWorkloadProgram, `if [ "$back" != "$payload" ]`)
		beat := strings.Index(podIOWorkloadProgram, `journal_line "ok $seq 0 $(now_ms) $c"`)

		Expect(write).To(BeNumerically(">", 0))
		Expect(sync).To(BeNumerically(">", write), "an unchecked flush is a beat that proves nothing")
		Expect(readBack).To(BeNumerically(">", sync))
		Expect(compare).To(BeNumerically(">", readBack))
		Expect(beat).To(BeNumerically(">", compare))
	})

	It("timestamps a beat after the verified cycle, exactly as the node-level writer does", func() {
		// A beat stamped BEFORE the write dates a freeze back to the moment the
		// frozen iteration began, so a freeze that ended while the stop flag was
		// already up sits inside no inter-beat gap at all — and the writer reports
		// itself healthy for the one outage the workload exists to catch.
		Expect(podIOWorkloadProgram).To(ContainSubstring(`journal_line "ok $seq 0 $(now_ms) $c"`))
		Expect(podIOWorkloadProgram).NotTo(ContainSubstring(`journal_line "ok $seq 0 $ts $c"`))
		Expect(ioWorkloadProgram).To(ContainSubstring(`journal("ok %d %d %d %08x" % (seq, slot, now_ms(), crc))`),
			"the journal format is shared, so both writers must stamp a beat at the same point")
	})

	It("writes the data file once and hashes it before recording the digest", func() {
		guard := strings.Index(podIOWorkloadProgram, `if [ ! -f "$sum" ]; then`)
		hash := strings.Index(podIOWorkloadProgram, `d=$(sha256sum <"$data.tmp"`)
		record := strings.Index(podIOWorkloadProgram, `printf '%s\n' "$d" >"$sum"`)

		Expect(guard).To(BeNumerically(">", 0),
			"a restarted container must verify the ORIGINAL bytes, not rewrite them")
		Expect(hash).To(BeNumerically(">", guard))
		Expect(record).To(BeNumerically(">", hash))
	})
})

// The specs below run the real writer in a temporary directory. That is what
// pins the program to the parser: the journal it produces has to be the journal
// parseIOJournal reads, the resumed sequence has to stay monotonic, and the
// digest it records has to be the digest of the file it wrote.
var _ = Describe("PodIOWorkload writer program, executed", Ordered, func() {
	// A four-record data file and a three-second run: enough for the data phase
	// and a couple of beats, short enough to run in every suite.
	const (
		writerRecords = 4
		writerBudget  = 3 * time.Second
	)

	var dir string
	var journal, data, sum, stop string

	BeforeAll(func() {
		for _, bin := range []string{"sh", "sha256sum", "date", "sed", "grep", "cut", "tail", "wc"} {
			if _, err := exec.LookPath(bin); err != nil {
				Skip("the writer program cannot be executed here: " + bin + " is missing")
			}
		}
		dir = filepath.Join(GinkgoT().TempDir(), "io")
		journal = filepath.Join(dir, "journal")
		data = filepath.Join(dir, "data")
		sum = filepath.Join(dir, "data.sha256")
		stop = filepath.Join(dir, "stop")
	})

	// runWriter runs the program until the budget kills it, and returns nothing
	// but the side effects it left in dir: the writer is a loop that never returns
	// on its own, exactly as it does not in the pod.
	//
	// Its output goes to a file and the kill goes to the whole process group,
	// because the writer's `sleep` runs as a child holding the same descriptors: a
	// pipe would keep the parent's Wait blocked on that surviving child (an hour,
	// for the sleep of an idling writer), and killing only the shell would leave it
	// behind.
	runWriter := func() {
		GinkgoHelper()
		script := strings.Join([]string{
			"dir=" + dir,
			"interval=1",
			fmt.Sprintf("records=%d", writerRecords),
			"record='" + podIODataRecord + "'",
			"",
			podIOWorkloadProgram,
		}, "\n")

		tmp := GinkgoT().TempDir()
		path := filepath.Join(tmp, "writer.sh")
		Expect(os.WriteFile(path, []byte(script), 0o600)).To(Succeed())
		logPath := filepath.Join(tmp, "writer.log")
		log, err := os.Create(logPath)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = log.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), writerBudget)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", path)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second

		err = cmd.Run()

		// Killed by the budget is the expected outcome; anything else means the
		// writer returned, which it must never do.
		Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "writer returned: %s", readFileString(logPath))
		Expect(err).To(HaveOccurred())
	}

	It("writes a data file of exactly the size its record count implies, and records its digest", func() {
		runWriter()

		content, err := os.ReadFile(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(content).To(HaveLen(writerRecords*podIORecordSize),
			"the record must be exactly podIORecordSize bytes, or the data file size is a guess")
		recorded, err := os.ReadFile(sum)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(recorded))).To(Equal(sha256Hex(content)))
	})

	It("produces a journal the framework's parser reads, with beats one second apart", func() {
		text, err := os.ReadFile(journal)
		Expect(err).NotTo(HaveOccurred())

		j, err := parseIOJournal(string(text))

		Expect(err).NotTo(HaveOccurred())
		Expect(j.Started).To(BeTrue())
		Expect(len(j.Beats)).To(BeNumerically(">=", 2), "journal: %s", text)
		Expect(j.Beats[0].Sequence).To(Equal(int64(0)))
		gap, _ := j.maxInterBeatGap()
		Expect(gap).To(BeNumerically("<=", 2*time.Second))
	})

	It("resumes its sequence after a restart instead of starting over", func() {
		before, err := parseIOJournal(readFileString(journal))
		Expect(err).NotTo(HaveOccurred())
		lastBefore := before.Beats[len(before.Beats)-1].Sequence

		runWriter()

		after, err := parseIOJournal(readFileString(journal))
		Expect(err).NotTo(HaveOccurred(),
			"a restart that restarted the sequence would make the journal non-monotonic and unparsable")
		Expect(after.Beats[len(after.Beats)-1].Sequence).To(BeNumerically(">", lastBefore))
	})

	It("keeps the data file it already wrote, digest included", func() {
		content, err := os.ReadFile(data)
		Expect(err).NotTo(HaveOccurred())
		recorded := strings.TrimSpace(readFileString(sum))

		Expect(recorded).To(Equal(sha256Hex(content)),
			"the second run must not have rewritten the data behind the recorded digest")
	})

	It("drops a record the previous container was cut off in the middle of", func() {
		// A crash between the write and the fsync of a journal line leaves a
		// partial record. Left in place it would sit in the MIDDLE of the journal
		// after the next append, and a malformed record anywhere but the last line
		// makes the whole journal unparsable.
		Expect(appendToFile(journal, "ok 9999 0 175000")).To(Succeed())

		runWriter()

		j, err := parseIOJournal(readFileString(journal))
		Expect(err).NotTo(HaveOccurred())
		for _, beat := range j.Beats {
			Expect(beat.Sequence).To(BeNumerically("<", 9999))
		}
	})

	It("stops on the flag the framework raises, and idles instead of exiting", func() {
		Expect(os.WriteFile(stop, nil, 0o600)).To(Succeed())

		runWriter()

		j, err := parseIOJournal(readFileString(journal))
		Expect(err).NotTo(HaveOccurred())
		Expect(j.Termination).NotTo(BeNil(), "the writer must report its last word")
		Expect(j.Termination.Failed).To(BeFalse())
		Expect(j.Termination.Message).To(ContainSubstring("writer stopped on request"))
	})

	It("reports a volume it cannot even write to through the pod status", func() {
		// No journal can exist on an unwritable volume, so this is the one failure
		// the writer exits on: the pod's container status is then the only place
		// that can carry the reason.
		readOnly := filepath.Join(GinkgoT().TempDir(), "missing", "io")
		Expect(os.WriteFile(filepath.Dir(readOnly), []byte("not a directory"), 0o600)).To(Succeed())

		script := strings.Join([]string{
			"dir=" + readOnly, "interval=1", "records=4",
			"record='" + podIODataRecord + "'", "", podIOWorkloadProgram,
		}, "\n")
		path := filepath.Join(GinkgoT().TempDir(), "writer.sh")
		Expect(os.WriteFile(path, []byte(script), 0o600)).To(Succeed())

		out, err := exec.Command("sh", path).CombinedOutput()

		Expect(err).To(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("pod-io-workload: cannot create"))
	})
})

// A volume that freezes and a volume that answers a flush with EIO are the two
// failures this writer exists to catch, and a healthy temporary directory
// produces neither. The specs below put a STUB sync(1) on the writer's PATH and
// drive it: the stub IS the volume, so a freeze is a flush that blocks and a lost
// write is a flush that returns an error. Both make the writer's behaviour
// observable in the only place a cluster run can read it — its journal.
var _ = Describe("PodIOWorkload writer program, against a volume that stops flushing", func() {
	const (
		// The writer's clock has one-second resolution (`date +%s`), so a freeze
		// has to last several seconds to be measurable at all, and freezeGapFloor
		// leaves one second of truncation at each end of the window. A freeze
		// stamped the old way — before the frozen cycle rather than after it —
		// measures one beat interval, which is why the floor is well above it.
		freezeHold     = 5 * time.Second
		freezeGapFloor = 3 * time.Second

		// Three beat intervals are enough to catch a writer that kept beating.
		idleWatch = 3 * time.Second

		wait = 15 * time.Second
		poll = 100 * time.Millisecond
	)

	var dir, journal, beat, stop string
	// flag is what the spec raises to make the stubbed volume misbehave, and
	// flag+".frozen" is how the stub reports back that a flush is blocked in it.
	var flag, stubDir, logPath string

	BeforeEach(func() {
		for _, bin := range []string{"sh", "sha256sum", "date", "sed", "grep", "cut", "tail", "wc", "sleep"} {
			if _, err := exec.LookPath(bin); err != nil {
				Skip("the writer program cannot be executed here: " + bin + " is missing")
			}
		}
		// A shell that carries sync(1) as a BUILT-IN never consults PATH, so the
		// stub would never be reached and these specs would time out instead of
		// testing anything. Run them on a shell whose sync is a real binary.
		out, err := exec.Command("sh", "-c", "command -v sync").Output()
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "/") {
			Skip("the shell here does not resolve sync(1) through PATH, so the volume cannot be stubbed")
		}

		root := GinkgoT().TempDir()
		dir = filepath.Join(root, "io")
		journal = filepath.Join(dir, "journal")
		beat = filepath.Join(dir, "beat")
		stop = filepath.Join(dir, "stop")
		flag = filepath.Join(root, "flag")
		stubDir = filepath.Join(root, "bin")
		logPath = filepath.Join(root, "writer.log")
		Expect(os.MkdirAll(stubDir, 0o700)).To(Succeed())
	})

	// start installs stub as the sync(1) the writer will find and launches the
	// writer with it on PATH. Unlike runWriter above, the writer is LEFT RUNNING
	// and killed by a cleanup: these specs have to reach into the volume while a
	// flush of it is still in flight.
	//
	// The kill goes to the whole process group, because the writer's `sleep` — and
	// the stub blocking inside its own — are children holding the same
	// descriptors.
	start := func(stub string) {
		GinkgoHelper()
		Expect(os.WriteFile(filepath.Join(stubDir, "sync"), []byte(stub), 0o700)).To(Succeed())

		script := strings.Join([]string{
			"dir=" + dir, "interval=1", "records=4",
			"record='" + podIODataRecord + "'", "", podIOWorkloadProgram,
		}, "\n")
		path := filepath.Join(filepath.Dir(dir), "writer.sh")
		Expect(os.WriteFile(path, []byte(script), 0o600)).To(Succeed())
		log, err := os.Create(logPath)
		Expect(err).NotTo(HaveOccurred())

		cmd := exec.Command("sh", path)
		// The last PATH wins: os/exec keeps only the last value of a duplicated
		// environment key.
		cmd.Env = append(os.Environ(), "PATH="+stubDir+":"+os.Getenv("PATH"))
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		Expect(cmd.Start()).To(Succeed())
		DeferCleanup(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			_ = log.Close()
		})
	}

	// journalNow reads the journal the way the framework's probe does. Reading it
	// while the writer appends is safe: parseIOJournal tolerates a partial LAST
	// record, which is exactly what a read that raced an append sees.
	journalNow := func(g Gomega) ioJournal {
		text, err := os.ReadFile(journal)
		g.Expect(err).NotTo(HaveOccurred())
		j, err := parseIOJournal(string(text))
		g.Expect(err).NotTo(HaveOccurred())
		return j
	}

	// because carries the whole diagnosis a failure here has — a shell writer
	// leaves nothing else behind. It is handed to Gomega as a func() string, which
	// Gomega calls lazily: a description built eagerly would report the journal as
	// it was when the assertion STARTED, which for a timed-out Eventually is the
	// one state that explains nothing.
	because := func(why string) func() string {
		return func() string {
			text, _ := os.ReadFile(journal)
			log, _ := os.ReadFile(logPath)
			return fmt.Sprintf("%s\njournal:\n%swriter output:\n%s", why, text, log)
		}
	}

	It("leaves a freeze that ended after the stop flag went up inside an inter-beat gap", func() {
		// The stub blocks the flush of the BEAT file only. That is where a frozen
		// volume stalls the writer, and blocking there rather than in the journal's
		// own flush is what makes the frozen cycle the one whose beat is published
		// after the freeze — the case a beat stamped before its cycle loses.
		start(strings.Join([]string{
			`#!/bin/sh`,
			`if [ "$2" = "` + beat + `" ] && [ -f "` + flag + `" ]; then`,
			`    : >"` + flag + `.frozen"`,
			`    while [ -f "` + flag + `" ]; do sleep 1; done`,
			`fi`,
			`exit 0`,
		}, "\n"))

		By("letting the writer beat, then freezing the flush of its next beat")
		Eventually(func(g Gomega) int { return len(journalNow(g).Beats) }).
			WithTimeout(wait).WithPolling(poll).Should(BeNumerically(">=", 2))
		Expect(os.WriteFile(flag, nil, 0o600)).To(Succeed())
		Eventually(func() bool {
			_, err := os.Stat(flag + ".frozen")
			return err == nil
		}).WithTimeout(wait).WithPolling(poll).Should(BeTrue(),
			because("the stub was never asked to flush the beat file"))

		By("raising the stop flag while the volume is still frozen, and holding the freeze")
		Expect(os.WriteFile(stop, nil, 0o600)).To(Succeed())
		time.Sleep(freezeHold) // there is nothing to poll for: the freeze IS the wait
		Expect(os.Remove(flag)).To(Succeed())

		By("waiting for the beat the freeze delayed, and for the writer to stop")
		var j ioJournal
		Eventually(func(g Gomega) *ioTermination {
			j = journalNow(g)
			return j.Termination
		}).WithTimeout(wait).WithPolling(poll).ShouldNot(BeNil(),
			because("the writer never published the delayed beat and stopped"))

		gap, endedBy := j.maxInterBeatGap()

		Expect(j.Termination.Failed).To(BeFalse(),
			"the freeze ended before the stop was served, so the writer must stop cleanly: %s", j.Termination.Message)
		Expect(gap).To(BeNumerically(">=", freezeGapFloor),
			because("a freeze that ended inside the stop window is reported by nobody unless it lands in this gap"))
		Expect(endedBy).NotTo(BeNil())
		Expect(endedBy.Sequence).To(Equal(j.Beats[len(j.Beats)-1].Sequence),
			"the freeze must show as the gap the LAST beat ended, not as an earlier one")
	})

	It("dies on a beat whose flush returned EIO instead of beating on", func() {
		// The stub succeeds until the flag is raised, so the writer still picks the
		// per-file flush at startup, and then answers the flush of the beat file the
		// way a volume that dropped the write does. NOTHING else fails: the
		// read-back is served from the page cache and the journal's own flush still
		// works, which is precisely why an unchecked — or globally retried — flush
		// would keep every beat after the EIO green.
		start(strings.Join([]string{
			`#!/bin/sh`,
			`if [ "$2" = "` + beat + `" ] && [ -f "` + flag + `" ]; then`,
			`    echo "sync: error syncing '` + beat + `': Input/output error" >&2`,
			`    exit 5`,
			`fi`,
			`exit 0`,
		}, "\n"))

		By("letting the writer beat, then making the volume drop the write")
		Eventually(func(g Gomega) int { return len(journalNow(g).Beats) }).
			WithTimeout(wait).WithPolling(poll).Should(BeNumerically(">=", 2))
		Expect(os.WriteFile(flag, nil, 0o600)).To(Succeed())

		By("expecting the failure in the journal instead of another beat")
		var j ioJournal
		Eventually(func(g Gomega) *ioTermination {
			j = journalNow(g)
			return j.Termination
		}).WithTimeout(wait).WithPolling(poll).ShouldNot(BeNil(),
			because("a flush that returned EIO left the journal green instead of killing the writer"))

		Expect(j.Termination.Failed).To(BeTrue())
		Expect(j.Termination.Message).To(ContainSubstring("flushing the beat file to the volume failed"))

		// The fail record must stay the LAST record: a beat journalled after it is a
		// green beat over a write the volume told the writer it had dropped.
		Consistently(func(g Gomega) string {
			text, err := os.ReadFile(journal)
			g.Expect(err).NotTo(HaveOccurred())
			lines := strings.Split(strings.TrimRight(string(text), "\n"), "\n")
			return lines[len(lines)-1]
		}).WithTimeout(idleWatch).WithPolling(poll).Should(HavePrefix("fail "))
	})
})

// Dropping the partial last record of a journal a dead container left behind is
// the one repair this writer performs, and a repair that quietly did nothing is
// worse than no repair at all: the writer would append to that journal anyway and
// leave the partial record in its MIDDLE, where it makes the whole history
// unreadable — or, cut at the right byte, where the next append completes it into
// a beat nobody ever verified. The specs below stub the tools the trim is made of,
// so the trim fails on a volume that is otherwise perfectly healthy, which is the
// only way that failure is reachable without breaking everything else with it.
var _ = Describe("PodIOWorkload writer program, whose journal cannot be trimmed", func() {
	const (
		wait = 15 * time.Second
		poll = 100 * time.Millisecond

		// Three beat intervals: long enough for a writer that trimmed nothing and
		// carried on to have appended its start record and a couple of beats.
		appendWatch = 3 * time.Second

		// The journal a dead container left behind: two published beats and a third
		// record cut in the middle of its timestamp. Cut THERE on purpose — the
		// fragment is then unparsable on its own, which is what parseIOJournal
		// tolerates on a last line, and unparsable after any append too, which is
		// what it refuses anywhere else.
		partialJournal = "start 1750000000000 7 /data/io/data 0:0 256\n" +
			"ok 0 0 1750000000001 aaaaaaaa\n" +
			"ok 1 0 1750000000002 bbbbbbbb\n" +
			"ok 2 0 17500"
	)

	var dir, journal, stubDir, logPath string

	BeforeEach(func() {
		for _, bin := range []string{"sh", "sha256sum", "date", "sed", "grep", "cut", "tail", "wc", "mv", "rm", "sleep"} {
			if _, err := exec.LookPath(bin); err != nil {
				Skip("the writer program cannot be executed here: " + bin + " is missing")
			}
		}
		// A shell that carries sed(1) or mv(1) as a BUILT-IN never consults PATH, so
		// the stub would never be reached: the trim would succeed and the spec would
		// wait out its timeouts instead of testing anything.
		for _, bin := range []string{"sed", "mv"} {
			out, err := exec.Command("sh", "-c", "command -v "+bin).Output()
			if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "/") {
				Skip("the shell here does not resolve " + bin + "(1) through PATH, so the trim cannot be stubbed")
			}
		}

		root := GinkgoT().TempDir()
		dir = filepath.Join(root, "io")
		journal = filepath.Join(dir, "journal")
		stubDir = filepath.Join(root, "bin")
		logPath = filepath.Join(root, "writer.log")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(stubDir, 0o700)).To(Succeed())
		Expect(os.WriteFile(journal, []byte(partialJournal), 0o600)).To(Succeed())
	})

	// start installs stub as the tool the writer will find under name and launches
	// the writer with it on PATH. The writer is LEFT RUNNING: it must idle after the
	// failure, not exit, so that the journal it refused to touch stays readable
	// through an exec into the container.
	start := func(name, stub string) {
		GinkgoHelper()
		Expect(os.WriteFile(filepath.Join(stubDir, name), []byte(stub), 0o700)).To(Succeed())

		script := strings.Join([]string{
			"dir=" + dir, "interval=1", "records=4",
			"record='" + podIODataRecord + "'", "", podIOWorkloadProgram,
		}, "\n")
		path := filepath.Join(filepath.Dir(dir), "writer.sh")
		Expect(os.WriteFile(path, []byte(script), 0o600)).To(Succeed())
		log, err := os.Create(logPath)
		Expect(err).NotTo(HaveOccurred())

		cmd := exec.Command("sh", path)
		// The last PATH wins: os/exec keeps only the last value of a duplicated key.
		cmd.Env = append(os.Environ(), "PATH="+stubDir+":"+os.Getenv("PATH"))
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		Expect(cmd.Start()).To(Succeed())
		DeferCleanup(func() {
			// The whole group: the idling writer's `sleep` is a child of it.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			_ = log.Close()
		})
	}

	DescribeTable("dies with the reason instead of appending to the journal anyway",
		func(name string, stub func() string, want string) {
			start(name, stub())

			By("waiting for the writer to report the trim it could not do")
			Eventually(func() string {
				log, _ := os.ReadFile(logPath)
				return string(log)
			}).WithTimeout(wait).WithPolling(poll).Should(ContainSubstring("pod-io-workload: "+want),
				"the writer must name the step that failed, not carry on silently")

			By("expecting nothing appended to a journal whose last record is still partial")
			Consistently(func(g Gomega) string {
				content, err := os.ReadFile(journal)
				g.Expect(err).NotTo(HaveOccurred())
				return string(content)
			}).WithTimeout(appendWatch).WithPolling(poll).Should(Equal(partialJournal),
				"an append here puts the partial record in the MIDDLE of the journal, which is what the trim exists to prevent")

			// The point of dying here: everything the dead container verified is still
			// evidence. A record appended over the partial one would have cost all of it.
			j, err := parseIOJournal(readFileString(journal))
			Expect(err).NotTo(HaveOccurred(), "the journal the writer left behind must stay readable")
			Expect(j.Beats).To(HaveLen(2))
			Expect(j.Beats[len(j.Beats)-1].Sequence).To(Equal(int64(1)),
				"the partial record must be neither dropped from nor completed into the history")
			// No `fail` record, deliberately: it could only be appended to the partial
			// record itself. The container's log carries the reason instead.
			Expect(j.Termination).To(BeNil())

			_, err = os.Stat(journal + ".trim")
			Expect(err).To(MatchError(os.ErrNotExist),
				"a half-written trim copy left on the volume is the space the next writer needs")
		},
		Entry("the partial record cannot be dropped", "sed",
			func() string { return "#!/bin/sh\nexit 1\n" },
			"journal: dropping the partial last record failed"),
		Entry("the trimmed copy cannot replace the journal", "mv",
			func() string {
				GinkgoHelper()
				mv, err := exec.LookPath("mv")
				Expect(err).NotTo(HaveOccurred())
				// Only the trim's own mv fails. The data file is renamed with the same
				// mv, and a stub that failed everywhere would kill the writer in the
				// data phase, long before it reaches the journal.
				return strings.Join([]string{
					"#!/bin/sh",
					`if [ "$3" = "` + journal + `" ]; then exit 1; fi`,
					`exec ` + mv + ` "$@"`,
					"",
				}, "\n")
			},
			"journal: replacing the journal with its trimmed copy failed"),
	)
})

// shellSyntaxError runs `sh -n` over script and reports what it said.
func shellSyntaxError(script string) error {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "script.sh")
	Expect(os.WriteFile(path, []byte(script), 0o600)).To(Succeed())

	out, err := exec.Command("sh", "-n", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sh -n: %v: %s", err, out)
	}
	return nil
}

// sha256Hex is the digest the writer's sha256sum produces for the same bytes.
func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func readFileString(path string) string {
	GinkgoHelper()
	content, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

func appendToFile(path, text string) error {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()
	_, err = fh.WriteString(text)
	return err
}
