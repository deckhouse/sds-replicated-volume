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
	"encoding/base64"
	"fmt"
	"strings"
)

// ioWorkloadDir holds the writer's program, marker and journal on the node.
// It deliberately lives outside /run: a journal that survives a node reboot
// still tells us when writes stopped, and the marker's boot id keeps a
// pre-reboot entry from ever being mistaken for a live process.
const ioWorkloadDir = "/var/tmp/sds-rv-e2e-io"

// spawnedMarker is printed by the spawn command after the writer was forked.
const spawnedMarker = "#spawned"

// ioWorkloadProgram is the persistent writer that runs on the node.
//
// It is delivered as a file (base64 over the exec channel) and started with
// setsid, so it outlives the exec session that spawned it. Its contract:
//
//   - publish the lock/marker atomically (O_EXCL semantics via link(2)) BEFORE
//     the device is opened, so a writer either does not exist or is findable
//     by run id, whatever happens to the exec that spawned it;
//   - refuse to start when a marker for the same run id already exists —
//     a repeated spawn can never produce a second writer;
//   - own the journal: truncate it once the marker is held, so one journal
//     always holds exactly one writer's monotonic sequence;
//   - open the device exactly once, verify identity with fstat on that
//     descriptor (block device, DRBD major, expected minor) and do all I/O
//     through the same descriptor, leaving no window between check and write;
//   - write only aligned, slot-sized records, only inside the bounded ring;
//   - publish a heartbeat only after write -> fdatasync -> read-back ->
//     checksum comparison, so a heartbeat proves a device write.
const ioWorkloadProgram = `import errno
import json
import os
import signal
import stat
import struct
import sys
import time
import zlib

MAGIC = b"SDSRVIO1"
DRBD_MAJOR = 147

RUN_ID = sys.argv[1]
DEVICE = sys.argv[2]
EXPECTED_MINOR = int(sys.argv[3])
JOURNAL = sys.argv[4]
MARKER = sys.argv[5]
SLOTS = int(sys.argv[6])
SLOT_SIZE = int(sys.argv[7])
INTERVAL_MS = int(sys.argv[8])


def now_ms():
    return int(time.time() * 1000)


def journal(line):
    with open(JOURNAL, "a") as fh:
        fh.write(line + "\n")
        fh.flush()
        os.fsync(fh.fileno())


def proc_start_time(pid):
    with open("/proc/%d/stat" % pid) as fh:
        data = fh.read()
    return data[data.rindex(")") + 1:].split()[19]


def boot_id():
    try:
        with open("/proc/sys/kernel/random/boot_id") as fh:
            return fh.read().strip()
    except EnvironmentError:
        return ""


def publish_marker():
    payload = json.dumps({
        "runID": RUN_ID,
        "pid": os.getpid(),
        "procStartTime": proc_start_time(os.getpid()),
        "bootID": boot_id(),
        "device": DEVICE,
        "journal": JOURNAL,
    })
    tmp = "%s.tmp.%d" % (MARKER, os.getpid())
    with open(tmp, "w") as fh:
        fh.write(payload)
        fh.flush()
        os.fsync(fh.fileno())
    try:
        os.link(tmp, MARKER)
    except OSError as exc:
        os.unlink(tmp)
        if exc.errno == errno.EEXIST:
            return False
        raise
    os.unlink(tmp)
    return True


def drop_marker():
    try:
        os.unlink(MARKER)
    except OSError:
        pass


def die(message):
    journal("fail %d %s" % (now_ms(), message))
    drop_marker()
    sys.stderr.write(message + "\n")
    sys.exit(1)


def open_verified():
    fd = os.open(DEVICE, os.O_RDWR)
    info = os.fstat(fd)
    if not stat.S_ISBLK(info.st_mode):
        os.close(fd)
        die("identity: %s is not a block device" % DEVICE)
    major = os.major(info.st_rdev)
    minor = os.minor(info.st_rdev)
    if major != DRBD_MAJOR or minor != EXPECTED_MINOR:
        os.close(fd)
        die("identity: %s is %d:%d, expected %d:%d" % (DEVICE, major, minor, DRBD_MAJOR, EXPECTED_MINOR))
    size = os.lseek(fd, 0, os.SEEK_END)
    if size < SLOTS * SLOT_SIZE:
        os.close(fd)
        die("ring: device size %d is smaller than the ring %d" % (size, SLOTS * SLOT_SIZE))
    journal("start %d %d %s %d:%d %d" % (now_ms(), os.getpid(), DEVICE, major, minor, size))
    return fd


def build_record(seq, slot):
    body = MAGIC + struct.pack("<qqq", seq, slot, now_ms()) + RUN_ID.encode()
    body = body[:SLOT_SIZE - 4].ljust(SLOT_SIZE - 4, b"\x00")
    crc = zlib.crc32(body) & 0xffffffff
    return body + struct.pack("<I", crc), crc


def main():
    if not publish_marker():
        sys.stderr.write("marker %s already exists; not starting a second writer\n" % MARKER)
        return 0

    # Own the journal from scratch: only the process holding the marker gets
    # here, and mixing two writers' sequences in one file would make the tail
    # unreadable.
    with open(JOURNAL, "w"):
        pass

    stopping = []
    signal.signal(signal.SIGTERM, lambda *_: stopping.append(True))
    signal.signal(signal.SIGINT, lambda *_: stopping.append(True))

    fd = open_verified()
    seq = 0
    while not stopping:
        slot = seq % SLOTS
        offset = slot * SLOT_SIZE
        buf, crc = build_record(seq, slot)
        try:
            os.pwrite(fd, buf, offset)
            os.fdatasync(fd)
            try:
                os.posix_fadvise(fd, offset, SLOT_SIZE, os.POSIX_FADV_DONTNEED)
            except (AttributeError, OSError):
                pass
            back = os.pread(fd, SLOT_SIZE, offset)
        except EnvironmentError as exc:
            die("io: sequence %d: %s" % (seq, exc))
        if back != buf:
            die("readback: sequence %d in slot %d differs from what was written" % (seq, slot))
        journal("ok %d %d %d %08x" % (seq, slot, now_ms(), crc))
        seq += 1
        time.sleep(INTERVAL_MS / 1000.0)

    journal("stopped %d %d" % (seq, now_ms()))
    drop_marker()
    return 0


sys.exit(main())
`

// programPath, markerPath, journalPath and spawnLogPath are the writer's files
// on the node. They are addressed by run id, which is what makes every
// operation find exactly this run's writer.
func (w *IOWorkload) programPath() string  { return ioWorkloadDir + "/" + w.opts.RunID + ".py" }
func (w *IOWorkload) markerPath() string   { return ioWorkloadDir + "/" + w.opts.RunID + ".marker" }
func (w *IOWorkload) journalPath() string  { return ioWorkloadDir + "/" + w.opts.RunID + ".journal" }
func (w *IOWorkload) spawnLogPath() string { return ioWorkloadDir + "/" + w.opts.RunID + ".spawn.log" }

// spawnCommand delivers the writer program to the node and starts it detached
// from the exec session. Running it twice is harmless: the program itself
// refuses to start a second writer for the same run id.
func (w *IOWorkload) spawnCommand() []string {
	program := base64.StdEncoding.EncodeToString([]byte(ioWorkloadProgram))
	script := strings.Join([]string{
		"set -e",
		"mkdir -p " + ioWorkloadDir,
		"command -v python3 >/dev/null 2>&1 || { echo 'the I/O workload needs python3 on the node' >&2; exit 127; }",
		"printf %s '" + program + "' | base64 -d > " + w.programPath() + ".tmp",
		"mv -f " + w.programPath() + ".tmp " + w.programPath(),
		fmt.Sprintf("setsid python3 %s %s %s %d %s %s %d %d %d </dev/null >>%s 2>&1 &",
			w.programPath(),
			w.opts.RunID,
			w.opts.DevicePath,
			w.minor,
			w.journalPath(),
			w.markerPath(),
			w.opts.Slots,
			w.opts.SlotSize,
			w.opts.Interval.Milliseconds(),
			w.spawnLogPath()),
		// Quoted: an unquoted word starting with '#' is a comment in sh.
		"printf '" + spawnedMarker + "\\n'",
	}, "\n")
	return []string{"sh", "-c", script}
}

// probeCommand collects everything one observation needs — the node's clock,
// its boot id, the marker, the raw /proc stat line of the marked pid and the
// journal tail — in a single short exec.
func (w *IOWorkload) probeCommand(tailLines int) []string {
	script := strings.Join([]string{
		// %3N is a GNU extension; a shell without it must still yield a number,
		// otherwise every observation would fail on the node's clock.
		`n=$(date +%s%3N 2>/dev/null)`,
		`case "$n" in ""|*[!0-9]*) n="$(date +%s)000" ;; esac`,
		`printf '#now %s\n' "$n"`,
		`printf '#boot %s\n' "$(cat /proc/sys/kernel/random/boot_id 2>/dev/null)"`,
		`m=$(cat ` + w.markerPath() + ` 2>/dev/null)`,
		`printf '#marker %s\n' "$m"`,
		`p=$(printf '%s' "$m" | sed -n 's/.*"pid":[ ]*\([0-9]*\).*/\1/p')`,
		`s=""`,
		`if [ -n "$p" ]; then s=$(cat /proc/$p/stat 2>/dev/null); fi`,
		`printf '#proc %s\n' "$s"`,
		`printf '#journal\n'`,
		fmt.Sprintf("tail -n %d %s 2>/dev/null", tailLines, w.journalPath()),
		"exit 0",
	}, "\n")
	return []string{"sh", "-c", script}
}

// signalCommand sends sig to the writer, but only after re-checking on the
// node that the pid still carries the recorded start time. The Go side already
// refuses to signal on a marker mismatch; this repeats the check next to the
// kill(2) so a pid recycled in between is still not hit.
func (w *IOWorkload) signalCommand(sig, expectedStartTime string) []string {
	script := strings.Join([]string{
		`p=$(sed -n 's/.*"pid":[ ]*\([0-9]*\).*/\1/p' ` + w.markerPath() + ` 2>/dev/null)`,
		`if [ -z "$p" ]; then printf '#signal no-marker\n'; exit 0; fi`,
		`s=$(cat /proc/$p/stat 2>/dev/null)`,
		`if [ -z "$s" ]; then printf '#signal no-process\n'; exit 0; fi`,
		`rest=${s##*) }`,
		`set -- $rest`,
		`if [ "${20}" != "` + expectedStartTime + `" ]; then printf '#signal start-time-mismatch\n'; exit 0; fi`,
		`if kill -` + sig + ` "$p"; then printf '#signal sent\n'; else printf '#signal failed\n'; fi`,
	}, "\n")
	return []string{"sh", "-c", script}
}

// clearMarkerCommand removes a marker whose writer is provably gone, so this
// run id can be started again. It removes the journal with it: the next writer
// restarts its sequence at 0, and two writers' records in one file would make
// the tail unreadable.
func (w *IOWorkload) clearMarkerCommand() []string {
	files := w.markerPath() + " " + w.markerPath() + ".tmp.* " + w.journalPath()
	return []string{"sh", "-c", "rm -f " + files + "; printf '#cleared\\n'"}
}

// purgeCommand removes everything this run left on the node.
func (w *IOWorkload) purgeCommand() []string {
	files := strings.Join([]string{
		w.markerPath(),
		w.markerPath() + ".tmp.*",
		w.journalPath(),
		w.programPath(),
		w.programPath() + ".tmp",
		w.spawnLogPath(),
	}, " ")
	return []string{"sh", "-c", "rm -f " + files + "; printf '#purged\\n'"}
}
