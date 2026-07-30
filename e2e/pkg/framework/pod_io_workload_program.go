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
	"strconv"
	"strings"
	"time"
)

const (
	// podIOMountPath is where the workload's PersistentVolumeClaim is mounted in
	// the writer pod.
	podIOMountPath = "/data"

	// podIODir holds everything the writer owns — and it deliberately lives ON
	// the volume under test: the journal is the evidence about that volume, so it
	// has to survive a restart of the container, and re-reading it after the
	// module was upgraded is exactly what proves the data path kept working.
	podIODir = podIOMountPath + "/io"

	// podIODataRecord is the record the deterministic data file is built from.
	// It is written with the record's index and is exactly podIORecordSize bytes
	// long, newline included, so the file size follows from the record count
	// alone.
	podIODataRecord = "sds-replicated-volume e2e upgrade data record: %016d\\n"

	// podIORecordSize is the byte length of one formatted podIODataRecord.
	podIORecordSize = 64

	// podIORecordsPerKiB is how many records make up one KiB of the data file.
	podIORecordsPerKiB = 1024 / podIORecordSize
)

// podIOWorkloadProgram is the writer that runs inside the pod. It is passed to
// `sh -c` as the container's command, prefixed with a header of variable
// assignments (see program), so the script itself stays a constant that unit
// tests can syntax-check and read.
//
// Its contract:
//
//   - write the one-shot data file ONCE per volume, deterministically, and
//     record its sha256 next to it. A restarted container must verify the
//     ORIGINAL bytes, so the file is regenerated only while no recorded digest
//     exists — and because the content is deterministic, a container that died
//     between writing the data and recording the digest produces the very same
//     file on its next attempt;
//   - resume the beat sequence from the journal, so the sequence stays strictly
//     monotonic across container restarts (parseIOJournal rejects anything else)
//     and a restart shows up as what it is: a GAP between two beats;
//   - publish a beat only after the beat record was written to the volume,
//     fsync'ed, read back and compared — the file-level counterpart of the
//     node-level writer's write/fdatasync/read-back/compare cycle. A beat
//     therefore proves the filesystem on the volume accepted and returned the
//     write; it does not prove a read from the device, which no portable shell
//     can force. What makes a frozen volume visible is the fsync: it blocks
//     while the device does not complete writes, and every blocked second shows
//     up as an inter-beat gap;
//   - timestamp a beat when it is PUBLISHED, after the verified cycle, exactly
//     as the node-level writer does. A beat stamped before the write would date
//     a freeze back to the moment the frozen iteration began, and a freeze that
//     ended inside the cleanup's stop window would then leave no gap between any
//     two beats at all — the writer would report itself healthy for the one
//     outage the workload exists to catch;
//   - treat a failing fsync as a failure of the data path and die: a flush that
//     returns EIO is how a volume reports that it dropped the write, and the
//     read-back that follows is served from the page cache, so a swallowed
//     fsync error turns into a green beat over lost data;
//   - prefer a per-file fsync (`sync -d FILE`) over the global sync(2): a global
//     sync flushes every filesystem on the NODE, so one frozen volume would
//     stall the beats of every other workload on that node and the freeze would
//     be reported against volumes that are perfectly healthy. The global sync is
//     only the fallback for a shell whose sync takes no file argument, chosen
//     ONCE at startup — never a fallback for a flush that failed;
//   - never exit once the journal exists: the journal and the data file are read
//     by exec'ing into THIS container, so a container that exits (or crash-loops)
//     takes the only way to read its own evidence with it. A failure is
//     journalled as a `fail` record and then the container idles, which is what
//     lets the framework report the failure with its message instead of an
//     unexplained absence of beats;
//   - repair a journal that was cut mid-append before appending anything to it,
//     and treat a repair that FAILED as fatal: appending to a journal whose last
//     record is still partial is precisely what the repair exists to prevent.
//     That single failure is reported through the container's log instead of the
//     journal — see die_partial_journal.
const podIOWorkloadProgram = `set -u

journal=$dir/journal
data=$dir/data
sum=$dir/data.sha256
beat=$dir/beat
stop=$dir/stop

now_ms() { echo "$(date +%s)000"; }

# idle keeps the container alive so its journal and data file stay readable.
idle() { while true; do sleep 3600; done; }

# sync_file flushes ONE file and REPORTS whether that flush succeeded: its exit
# status is the only evidence the volume accepted the bytes, so every caller
# checks it. In file mode a failing 'sync -d' is a failure of the data path — EIO
# from the flush is how a volume that lost its quorum answers — and falling back
# to the global sync here would swallow it: sync(2) reports an error to nobody,
# ever, while the read-back that follows is served from the page cache. The
# fallback for a shell whose sync takes no file argument is chosen once, at
# startup, and lives nowhere else.
sync_file() {
    if [ "$sync_mode" = file ]; then
        sync -d "$1"
    else
        sync
    fi
}

journal_line() {
    printf '%s\n' "$1" >>"$journal" || return 1
    sync_file "$journal"
}

# die reports through the journal best-effort: what failed may be the flush of
# this very record, and then there is nothing left to report it with.
die() {
    journal_line "fail $(now_ms) $1"
    printf 'pod-io-workload: %s\n' "$1" >&2
    idle
}

# die_partial_journal reports the ONE failure the journal cannot carry: the trim
# below could not drop a record the previous container was cut off in the middle
# of. A 'fail' record appended there would be CONCATENATED onto that very partial
# record — at best unreadable, at worst completing it into a line that parses as a
# beat nobody ever verified. So the journal is left exactly as that container left
# it: a partial LAST record is what the framework's parser tolerates, which keeps
# every beat that container did publish readable through an exec into here. The
# reason goes to the container's log, the one place a writer that cannot journal
# can still be read. The half-written copy goes away with it: it is a prefix of a
# journal that is being kept anyway, and on a volume that ran out of space it is
# holding the only space left.
die_partial_journal() {
    rm -f "$journal.trim"
    printf 'pod-io-workload: %s\n' "$1" >&2
    idle
}

# A volume that cannot even be written to has no journal to explain itself: fail
# the container instead, so the pod status carries the reason.
mkdir -p "$dir" || { printf 'pod-io-workload: cannot create %s\n' "$dir" >&2; exit 1; }
: >"$beat" || { printf 'pod-io-workload: cannot write to %s\n' "$beat" >&2; exit 1; }

sync_mode=file
sync -d "$beat" 2>/dev/null || sync_mode=all

if [ ! -f "$sum" ]; then
    i=0
    while [ "$i" -lt "$records" ]; do
        printf "$record" "$i"
        i=$((i + 1))
    done >"$data.tmp" || die "data: writing the data file failed"
    sync_file "$data.tmp" || die "data: flushing the data file to the volume failed"
    d=$(sha256sum <"$data.tmp" | cut -d' ' -f1) || die "data: sha256sum failed"
    [ -n "$d" ] || die "data: sha256sum produced no digest"
    mv -f "$data.tmp" "$data" || die "data: renaming the data file failed"
    sync_file "$data" || die "data: flushing the renamed data file to the volume failed"
    printf '%s\n' "$d" >"$sum" || die "data: recording the digest failed"
    sync_file "$sum" || die "data: flushing the recorded digest to the volume failed"
fi

# A journal that does not end with a newline was cut mid-append when the previous
# container died. Drop that partial record: left in place it would sit in the
# MIDDLE of the journal after the next append, and a malformed record anywhere but
# the last line makes the whole journal unparsable — losing one beat is cheaper
# than losing the entire history.
#
# A trim that FAILED therefore cannot be survived: this writer would append to the
# journal regardless and produce exactly the state the trim exists to prevent —
# unreadable history, or a partial record silently completed into a beat. Each step
# names itself, because a trim that cannot be written and a trimmed copy that
# cannot replace the journal are different faults of the volume.
if [ -s "$journal" ] && [ -n "$(tail -c 1 "$journal")" ]; then
    sed '$d' "$journal" >"$journal.trim" ||
        die_partial_journal "journal: dropping the partial last record failed"
    mv -f "$journal.trim" "$journal" ||
        die_partial_journal "journal: replacing the journal with its trimmed copy failed"
    sync_file "$journal" || die "journal: flushing the trimmed journal to the volume failed"
fi

last=$(grep '^ok ' "$journal" 2>/dev/null | cut -d' ' -f2 | grep -E '^[0-9]+$' | tail -n 1)
[ -n "$last" ] || last=-1
seq=$((last + 1))

journal_line "start $(now_ms) $$ $data 0:0 $(wc -c <"$data")" ||
    die "journal: flushing the start record to the volume failed"

while true; do
    if [ -f "$stop" ]; then
        journal_line "stopped $seq $(now_ms)" ||
            die "journal: flushing the stopped record to the volume failed"
        idle
    fi

    # $ts dates the CONTENT of the record; the beat itself is timestamped below,
    # once the cycle that verified it has returned.
    ts=$(now_ms)
    payload="beat $seq $ts"
    printf '%s\n' "$payload" >"$beat" || die "io: writing the beat file failed at sequence $seq"
    sync_file "$beat" || die "io: flushing the beat file to the volume failed at sequence $seq"
    back=$(cat "$beat") || die "io: reading the beat file back failed at sequence $seq"
    if [ "$back" != "$payload" ]; then
        die "readback: sequence $seq differs from what was written"
    fi
    c=$(printf '%s\n' "$payload" | sha256sum | cut -c1-8)
    # A FRESH timestamp, taken here: every second the cycle above spent blocked
    # in the fsync of a frozen volume has to land in the distance to the previous
    # beat, or a freeze that ended while the stop flag was already up would be
    # reported by nobody.
    journal_line "ok $seq 0 $(now_ms) $c" ||
        die "journal: flushing beat $seq to the volume failed"
    seq=$((seq + 1))
    sleep "$interval"
done
`

// journalPath, dataPath, sumPath and stopPath are the writer's files on the
// volume.
func (w *PodIOWorkload) journalPath() string { return podIODir + "/journal" }
func (w *PodIOWorkload) dataPath() string    { return podIODir + "/data" }
func (w *PodIOWorkload) sumPath() string     { return podIODir + "/data.sha256" }
func (w *PodIOWorkload) stopPath() string    { return podIODir + "/stop" }

// dataRecords is how many records the data file holds, i.e. its size in
// podIORecordSize-byte units.
func (w *PodIOWorkload) dataRecords() int {
	return w.opts.DataKiB * podIORecordsPerKiB
}

// program renders the container command: a header binding the values this
// workload was configured with, followed by podIOWorkloadProgram.
//
// The interval is whole seconds — BusyBox `sleep` takes no fractional argument
// and `date` has no portable sub-second format, so a sub-second beat rate would
// be neither sleepable nor timestampable. It is floored at one second.
func (w *PodIOWorkload) program() string {
	interval := int(w.opts.Interval.Round(time.Second) / time.Second)
	if interval < 1 {
		interval = 1
	}
	header := strings.Join([]string{
		"dir=" + podIODir,
		"interval=" + strconv.Itoa(interval),
		"records=" + strconv.Itoa(w.dataRecords()),
		"record='" + podIODataRecord + "'",
	}, "\n")
	return header + "\n\n" + podIOWorkloadProgram
}

// probeCommand collects one observation: the pod's OWN clock and the tail of the
// journal, in a single exec. The clock comes from inside the pod on purpose —
// every gap is measured between timestamps of that one clock, never against the
// runner's, so a clock skew between the pod and the machine running the suite
// can never be mistaken for a freeze.
//
// The envelope is the one parseIOProbe reads (the #marker/#proc/#boot lines of
// the node-level writer have no counterpart here and are simply absent).
func (w *PodIOWorkload) probeCommand(tailLines int) []string {
	script := strings.Join([]string{
		`printf '#now %s\n' "$(date +%s)000"`,
		`printf '#journal\n'`,
		fmt.Sprintf("tail -n %d %s 2>/dev/null", tailLines, w.journalPath()),
		"exit 0",
	}, "\n")
	return []string{"sh", "-c", script}
}

// checksumCommand re-hashes the data file and prints the digest recorded when it
// was written, so the comparison — and the message a mismatch produces — is made
// in Go, where both values and the path can be reported.
func (w *PodIOWorkload) checksumCommand() []string {
	script := strings.Join([]string{
		`printf '#recorded %s\n' "$(cut -d' ' -f1 <` + w.sumPath() + ` 2>/dev/null)"`,
		`printf '#actual %s\n' "$(sha256sum <` + w.dataPath() + ` 2>/dev/null | cut -d' ' -f1)"`,
		`printf '#size %s\n' "$(wc -c <` + w.dataPath() + ` 2>/dev/null)"`,
		"exit 0",
	}, "\n")
	return []string{"sh", "-c", script}
}

// stopCommand raises the stop flag. The writer notices it at the top of its next
// iteration, journals its `stopped` record and idles — it does NOT exit, because
// the journal and the data file are read through this very container.
func (w *PodIOWorkload) stopCommand() []string {
	script := "mkdir -p " + podIODir + " && touch " + w.stopPath() + " && printf '#stopping\\n'"
	return []string{"sh", "-c", script}
}

// podIOChecksum is what checksumCommand reports about the data file.
type podIOChecksum struct {
	// Recorded is the digest the writer stored when it created the file, and
	// Actual the digest of the file as it is now. Either is empty when the
	// corresponding file is missing or unreadable.
	Recorded string
	Actual   string
	// Size is the current byte size of the data file, -1 when unknown.
	Size int64
}

// parsePodIOChecksum parses the envelope printed by checksumCommand.
func parsePodIOChecksum(out string) (podIOChecksum, error) {
	c := podIOChecksum{Size: -1}
	seen := false

	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(strings.TrimSuffix(line, "\r"), " ")
		value = strings.TrimSpace(value)
		switch key {
		case "#recorded":
			c.Recorded = value
			seen = true
		case "#actual":
			c.Actual = value
		case "#size":
			if value == "" {
				continue
			}
			size, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return podIOChecksum{}, fmt.Errorf("checksum probe: unparsable data size %q: %w", value, err)
			}
			c.Size = size
		}
	}

	if !seen {
		return podIOChecksum{}, fmt.Errorf("checksum probe: output carries no #recorded line: %q", truncate(out, 512))
	}
	return c, nil
}
