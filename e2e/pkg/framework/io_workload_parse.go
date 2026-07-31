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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// drbdDevicePathRe matches the canonical DRBD device node. Anything else —
// an LVM path, a regular file, a device of another driver — is refused before
// the device is opened.
var drbdDevicePathRe = regexp.MustCompile(`^/dev/drbd([0-9]+)$`)

// parseDRBDDevicePath validates a resolved device path and returns its minor.
func parseDRBDDevicePath(path string) (int, error) {
	m := drbdDevicePathRe.FindStringSubmatch(strings.TrimSpace(path))
	if m == nil {
		return 0, fmt.Errorf("resolved device path %q is not a DRBD device node (want /dev/drbd<N>)", path)
	}
	minor, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("resolved device path %q carries an unparsable minor: %w", path, err)
	}
	return minor, nil
}

// parseDRBDMinorFromStatus extracts the minor the kernel assigned to
// resourceName. This is the ground truth for device identity: it comes from
// the kernel, not from the API objects whose device path we are validating.
func parseDRBDMinorFromStatus(out, resourceName string) (int, error) {
	st, err := parseDRBDStatus(out, resourceName)
	if err != nil {
		return 0, err
	}
	return st.Minor, nil
}

// parseProcStartTime returns field 22 (starttime) of a /proc/<pid>/stat line.
// The comm field may itself contain spaces and parentheses, so parsing starts
// after its closing parenthesis.
func parseProcStartTime(stat string) (string, error) {
	stat = strings.TrimSpace(stat)
	idx := strings.LastIndex(stat, ")")
	if idx < 0 {
		return "", fmt.Errorf("malformed /proc stat line %q: no comm field", truncate(stat, 256))
	}
	fields := strings.Fields(stat[idx+1:])
	// fields[0] is field 3 (state), so field 22 sits at index 19.
	const startTimeIndex = 19
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("malformed /proc stat line %q: only %d fields after comm", truncate(stat, 256), len(fields))
	}
	return fields[startTimeIndex], nil
}

// ioMarker is the lock file the writer publishes — atomically, before it opens
// the device — so that every later operation can find exactly that process.
type ioMarker struct {
	RunID         string `json:"runID"`
	PID           int    `json:"pid"`
	ProcStartTime string `json:"procStartTime"`
	BootID        string `json:"bootID"`
	Device        string `json:"device"`
	Journal       string `json:"journal"`
}

// parseIOMarker decodes a marker file. An empty input means "no marker".
func parseIOMarker(s string) (*ioMarker, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var m ioMarker
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("parsing workload marker %q: %w", truncate(s, 512), err)
	}
	if m.RunID == "" || m.PID <= 0 {
		return nil, fmt.Errorf("workload marker %q lacks runID or pid", truncate(s, 512))
	}
	return &m, nil
}

// matchIOMarker reports whether the process described by the marker is the very
// process this run started and is still alive — the only case in which it may
// be signalled. A different boot ID means the node rebooted; a different
// process start time means the PID was reused by an unrelated process.
//
// The second return value explains a negative match and is empty on a match.
func matchIOMarker(m *ioMarker, runID, bootID, procStat string) (bool, string) {
	switch {
	case m == nil:
		return false, "no marker file for this run"
	case m.RunID != runID:
		return false, fmt.Sprintf("marker belongs to run %q, not %q", m.RunID, runID)
	case bootID != "" && m.BootID != "" && m.BootID != bootID:
		return false, fmt.Sprintf("marker was written on boot %q, node is now on boot %q (node rebooted)", m.BootID, bootID)
	case strings.TrimSpace(procStat) == "":
		return false, fmt.Sprintf("process %d is gone", m.PID)
	}

	startTime, err := parseProcStartTime(procStat)
	if err != nil {
		return false, err.Error()
	}
	if startTime != m.ProcStartTime {
		return false, fmt.Sprintf("pid %d started at %q, marker recorded %q (pid reused)", m.PID, startTime, m.ProcStartTime)
	}
	return true, ""
}

// ioBeat is one verified write: the record was written to the device,
// fdatasync'ed, read back and checksum-compared before the beat was journalled.
type ioBeat struct {
	Sequence int64
	Slot     int
	At       time.Time
	Checksum string
}

// ioTermination is the writer's last word: a clean stop or a failure.
type ioTermination struct {
	Failed  bool
	At      time.Time
	Message string
}

// ioJournal is the parsed tail of the on-node heartbeat journal.
type ioJournal struct {
	Started     bool
	Beats       []ioBeat
	Termination *ioTermination
}

// last returns the newest beat, or nil when the tail holds none.
func (j ioJournal) last() *ioBeat {
	if len(j.Beats) == 0 {
		return nil
	}
	return &j.Beats[len(j.Beats)-1]
}

// maxInterBeatGap returns the largest distance between two consecutive beats
// in the observed tail, and the beat that ended that gap. Zero and nil with
// fewer than two beats. This is what makes stalls HISTORICAL evidence: a
// freeze that ended before the probe still shows as a gap between the last
// pre-freeze and the first post-freeze beat, for as long as that boundary
// stays within the observed tail.
func (j ioJournal) maxInterBeatGap() (gap time.Duration, endedBy *ioBeat) {
	for i := 1; i < len(j.Beats); i++ {
		if d := j.Beats[i].At.Sub(j.Beats[i-1].At); d > gap {
			gap, endedBy = d, &j.Beats[i]
		}
	}
	return gap, endedBy
}

// ioGap is one interval in which the writer published no beat — a window in
// which nothing reached the device.
//
// It lives next to the journal rather than next to one of the workloads
// because both of them (the node writer and the pod writer) read the same
// journal grammar and need the same measurement: how many times the data path
// stopped for longer than tolerated, and for how long.
type ioGap struct {
	// Duration is the distance between the two beats that bound the gap.
	Duration time.Duration
	// From and To are those beats' timestamps, on the writer's own clock.
	From time.Time
	To   time.Time
	// FromSequence and ToSequence are their sequence numbers, which is what
	// makes a gap findable in the journal.
	FromSequence int64
	ToSequence   int64
}

// String renders the gap for failure messages.
func (g ioGap) String() string {
	return fmt.Sprintf("%s between beats %d and %d (%s .. %s)",
		g.Duration.Truncate(time.Millisecond), g.FromSequence, g.ToSequence,
		g.From.Format("15:04:05"), g.To.Format("15:04:05"))
}

// gapsOver lists every gap between two consecutive verified writes that is
// longer than threshold, oldest first. The COUNT matters as much as the
// durations: a policy that tolerates one expected freeze has to be able to tell
// one long gap from several.
func (j ioJournal) gapsOver(threshold time.Duration) []ioGap {
	var out []ioGap
	for i := 1; i < len(j.Beats); i++ {
		prev, cur := j.Beats[i-1], j.Beats[i]
		gap := cur.At.Sub(prev.At)
		if gap <= threshold {
			continue
		}
		out = append(out, ioGap{
			Duration:     gap,
			From:         prev.At,
			To:           cur.At,
			FromSequence: prev.Sequence,
			ToSequence:   cur.Sequence,
		})
	}
	return out
}

// malformedRecordError marks a record that could not be parsed at all. The
// tail may have caught the writer mid-append, so this is tolerated on the last
// line — unlike a semantic violation such as a non-monotonic sequence, which
// is a real defect wherever it appears.
type malformedRecordError struct{ err error }

func (e malformedRecordError) Error() string { return e.err.Error() }
func (e malformedRecordError) Unwrap() error { return e.err }

func malformed(format string, args ...any) error {
	return malformedRecordError{err: fmt.Errorf(format, args...)}
}

// parseIOJournal parses a journal tail. Record grammar:
//
//	start   <unixms> <pid> <device> <major:minor> <deviceSize>
//	ok      <sequence> <slot> <unixms> <crc32>
//	fail    <unixms> <message...>
//	stopped <sequence> <unixms>
//
// The tail is produced by `tail -n`, so every line is whole — except possibly
// the last one, which the writer may be appending right now; an unparsable last
// line is therefore ignored, while an unparsable line anywhere else is an
// error. Sequence numbers of consecutive beats must strictly increase.
func parseIOJournal(text string) (ioJournal, error) {
	var j ioJournal

	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // trailing newline, not a partial record
	}

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		err := j.appendRecord(line)
		if err == nil {
			continue
		}
		var incomplete malformedRecordError
		if i == len(lines)-1 && errors.As(err, &incomplete) {
			continue // the writer is mid-append; the record will be whole next time
		}
		return ioJournal{}, err
	}
	return j, nil
}

// appendRecord parses one journal line into the journal.
func (j *ioJournal) appendRecord(line string) error {
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return malformed("empty journal record")

	case fields[0] == "start":
		if len(fields) < 2 {
			return malformed("malformed start record %q", line)
		}
		if _, err := parseUnixMillis(fields[1]); err != nil {
			return malformed("malformed start record %q: %v", line, err)
		}
		j.Started = true
		return nil

	case fields[0] == "ok":
		beat, err := parseIOBeat(fields, line)
		if err != nil {
			return err
		}
		if prev := j.last(); prev != nil && beat.Sequence <= prev.Sequence {
			return fmt.Errorf("journal sequence is not monotonic: %d follows %d", beat.Sequence, prev.Sequence)
		}
		j.Beats = append(j.Beats, beat)
		return nil

	case fields[0] == "fail":
		if len(fields) < 3 {
			return malformed("malformed fail record %q", line)
		}
		at, err := parseUnixMillis(fields[1])
		if err != nil {
			return malformed("malformed fail record %q: %v", line, err)
		}
		j.Termination = &ioTermination{Failed: true, At: at, Message: strings.Join(fields[2:], " ")}
		return nil

	case fields[0] == "stopped":
		if len(fields) < 3 {
			return malformed("malformed stopped record %q", line)
		}
		at, err := parseUnixMillis(fields[2])
		if err != nil {
			return malformed("malformed stopped record %q: %v", line, err)
		}
		j.Termination = &ioTermination{At: at, Message: "writer stopped on request after " + fields[1] + " writes"}
		return nil

	default:
		return malformed("unknown journal record %q", line)
	}
}

// parseIOBeat parses an "ok" record.
func parseIOBeat(fields []string, line string) (ioBeat, error) {
	if len(fields) < 5 {
		return ioBeat{}, malformed("malformed ok record %q", line)
	}
	seq, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return ioBeat{}, malformed("malformed ok record %q: sequence: %v", line, err)
	}
	slot, err := strconv.Atoi(fields[2])
	if err != nil {
		return ioBeat{}, malformed("malformed ok record %q: slot: %v", line, err)
	}
	at, err := parseUnixMillis(fields[3])
	if err != nil {
		return ioBeat{}, malformed("malformed ok record %q: %v", line, err)
	}
	return ioBeat{Sequence: seq, Slot: slot, At: at, Checksum: fields[4]}, nil
}

// ioProbe is one observation of the workload: the node's own clock, its boot
// id, the marker, the raw /proc stat line of the marked pid, and the journal
// tail — everything a decision needs, collected by a single exec.
type ioProbe struct {
	Now      time.Time
	BootID   string
	Marker   *ioMarker
	ProcStat string
	Journal  ioJournal
}

// isEmpty reports that the node holds no trace of the run at all: no marker and
// not a single journal record. It is what tells "already purged" apart from
// "the writer is there and wrote nothing".
func (p ioProbe) isEmpty() bool {
	return p.Marker == nil && !p.Journal.Started &&
		len(p.Journal.Beats) == 0 && p.Journal.Termination == nil
}

// parseIOProbe parses the envelope printed by the probe command.
func parseIOProbe(out string) (ioProbe, error) {
	var p ioProbe
	var journalStarted bool
	var journal strings.Builder

	for _, line := range strings.Split(out, "\n") {
		if journalStarted {
			journal.WriteString(line)
			journal.WriteString("\n")
			continue
		}
		key, value, _ := strings.Cut(strings.TrimSuffix(line, "\r"), " ")
		switch key {
		case "#now":
			now, err := parseUnixMillis(strings.TrimSpace(value))
			if err != nil {
				return ioProbe{}, fmt.Errorf("probe: node clock: %w", err)
			}
			p.Now = now
		case "#boot":
			p.BootID = strings.TrimSpace(value)
		case "#marker":
			marker, err := parseIOMarker(value)
			if err != nil {
				return ioProbe{}, fmt.Errorf("probe: %w", err)
			}
			p.Marker = marker
		case "#proc":
			p.ProcStat = strings.TrimSpace(value)
		case "#journal":
			journalStarted = true
		}
	}

	if p.Now.IsZero() {
		return ioProbe{}, fmt.Errorf("probe: output carries no #now line: %q", truncate(out, 512))
	}
	parsed, err := parseIOJournal(journal.String())
	if err != nil {
		return ioProbe{}, fmt.Errorf("probe: %w", err)
	}
	p.Journal = parsed
	return p, nil
}

// parseUnixMillis parses a Unix timestamp printed by the node. `date +%s%3N`
// yields milliseconds, but a shell without the %N extension yields seconds —
// both are accepted so a stall is never reported because of a date(1) quirk.
func parseUnixMillis(s string) (time.Time, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("unparsable timestamp %q: %w", s, err)
	}
	const millisThreshold = 1e11 // Unix seconds stay below this until year 5138
	if v < millisThreshold {
		return time.Unix(v, 0), nil
	}
	return time.UnixMilli(v), nil
}

// truncate shortens s for error messages.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
