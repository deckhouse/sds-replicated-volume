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
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

// Defaults of IOWorkloadOptions. The ring is deliberately tiny: it must fit
// into the smallest volume the suite creates, and its only job is to prove
// that writes keep reaching the device.
const (
	ioWorkloadDefaultSlots        = 16
	ioWorkloadDefaultSlotSize     = 4096
	ioWorkloadDefaultInterval     = 200 * time.Millisecond
	ioWorkloadDefaultMaxGap       = 30 * time.Second
	ioWorkloadDefaultStartTimeout = 90 * time.Second
	ioWorkloadDefaultStopTimeout  = 30 * time.Second
	ioWorkloadDefaultPoll         = 500 * time.Millisecond

	ioWorkloadJournalTail   = 40
	ioWorkloadSpawnAttempts = 3

	// ioWorkloadJournalFull is the tail size used for the final verification:
	// large enough to cover every beat a spec-length run can produce, so a
	// stall anywhere in the run is found even when its boundary has long left
	// the regular probe tail.
	ioWorkloadJournalFull = 1 << 20
)

// runIDRe and devicePathRe keep run ids and device paths to characters that are
// literal in the shell: both end up inside the commands built for the node.
var (
	runIDRe      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	devicePathRe = regexp.MustCompile(`^/dev/[A-Za-z0-9._/-]+$`)
)

// IOWorkloadOptions configures a raw-device writer.
type IOWorkloadOptions struct {
	// NodeName is the node whose host runs the writer. Required.
	NodeName string

	// DevicePath is the device to write to, as published in
	// RVA.Status.DevicePath. Required. It is validated (and resolved) before
	// anything is opened.
	DevicePath string

	// DRBDResourceName is the resource name as the kernel of NodeName knows it
	// (the agent's "sdsrv-" prefixed name). Required: the expected device minor
	// is read from drbdsetup for this resource, which is the only identity
	// source independent of the API objects being tested.
	DRBDResourceName string

	// RunID addresses this writer's marker, journal and program on the node.
	// Every operation locates the writer by run id, which is what makes start
	// idempotent and cleanup exact.
	//
	// Defaults to a name unique to this call, so two workloads started by one
	// spec never collide. Passing the same run id twice is how a spec asks for
	// the second start to adopt the writer of the first — it then MUST also
	// pass the same DevicePath, and MUST leave the writer to a single handle's
	// cleanup (see StartIOWorkload).
	RunID string

	// Slots and SlotSize bound the ring the writer cycles through. All writes
	// are SlotSize-aligned and stay inside Slots*SlotSize bytes.
	Slots    int
	SlotSize int

	// Interval is the pause between two verified writes.
	Interval time.Duration

	// MaxHeartbeatGap is the longest tolerated distance between the node's
	// clock and the last verified write before the workload counts as stalled.
	// The same bound applies HISTORICALLY, to the distance between any two
	// consecutive verified writes: a stall longer than this fails the workload
	// even when writes have resumed by the time anyone looks (checked on every
	// progress wait and, over the whole journal, at cleanup).
	MaxHeartbeatGap time.Duration

	// StartTimeout bounds the wait for the first verified write, StopTimeout
	// the wait for the writer to finish after SIGTERM.
	StartTimeout time.Duration
	StopTimeout  time.Duration
}

// IOWorkloadTermination is the writer's last journal record.
type IOWorkloadTermination struct {
	// Failed distinguishes an I/O or identity failure from a clean stop.
	Failed  bool
	At      time.Time
	Message string
}

// IOWorkloadStatus is a point-in-time view of the writer, computed from one
// probe of the node.
type IOWorkloadStatus struct {
	// Running reports that the marker of this run is present AND owned by a
	// live process with the recorded start time.
	Running bool

	// LastSequence is the sequence number of the last verified write, or -1
	// when the observed journal tail holds none.
	LastSequence int64
	LastWriteAt  time.Time

	// Gap is the node-clock age of the last verified write, and Stalled says
	// it exceeds MaxHeartbeatGap while the writer has not terminated.
	Gap     time.Duration
	Stalled bool

	// MaxObservedGap is the largest distance between two consecutive verified
	// writes in the observed journal tail. Unlike Gap it is historical: a
	// stall that already ended still shows here for as long as its boundary
	// stays within the observed tail (and always at the final verification,
	// which reads the whole journal). GapExceeded says it went over
	// MaxHeartbeatGap — the writer stopped writing for longer than the spec
	// tolerates at some point, even if it is writing again now.
	MaxObservedGap time.Duration
	GapExceeded    bool

	// Terminated is set once the writer wrote its last record.
	Terminated *IOWorkloadTermination

	// Note explains why Running is false.
	Note string

	probe ioProbe
}

// String renders the status for failure messages and logs.
func (s IOWorkloadStatus) String() string {
	parts := []string{
		fmt.Sprintf("running=%t", s.Running),
		fmt.Sprintf("lastSequence=%d", s.LastSequence),
	}
	if !s.LastWriteAt.IsZero() {
		parts = append(parts, fmt.Sprintf("gap=%s", s.Gap.Truncate(time.Millisecond)))
	}
	if s.Stalled {
		parts = append(parts, "stalled")
	}
	if s.GapExceeded {
		parts = append(parts, fmt.Sprintf("stalled-for=%s", s.MaxObservedGap.Truncate(time.Millisecond)))
	}
	if s.Terminated != nil {
		parts = append(parts, fmt.Sprintf("terminated(failed=%t)=%q", s.Terminated.Failed, s.Terminated.Message))
	}
	if s.Note != "" {
		parts = append(parts, fmt.Sprintf("note=%q", s.Note))
	}
	return strings.Join(parts, " ")
}

// IOWorkload is a persistent raw-device writer running on a node. It proves
// that I/O keeps reaching the device: every heartbeat it publishes is preceded
// by an aligned write, an fdatasync, a read-back and a checksum comparison.
//
// Obtain one from Framework.StartIOWorkload; its cleanup is registered before
// the writer is spawned, so a failing spec can never leave a process writing.
type IOWorkload struct {
	f    *Framework
	opts IOWorkloadOptions

	minor     int
	poll      time.Duration
	cleanedUp bool
}

// StartIOWorkload starts a raw-device writer on opts.NodeName and returns once
// it has completed its first verified write.
//
// The calling spec MUST carry LabelDisruptive, on itself or on an enclosing
// container: this spawns a process writing to a raw block device on the host of
// a shared cluster. The requirement is enforced, not merely stated —
// RequireDisruptiveSpec fails the spec before the options are even validated.
//
// Guarantees:
//   - Nothing is written before the device is proven to be this volume: the
//     path is resolved and must be the canonical /dev/drbd<N>, its minor must
//     equal the minor drbdsetup reports for opts.DRBDResourceName, and the
//     writer re-checks identity with fstat on the descriptor it will use.
//   - Starting is idempotent per run id. The writer publishes its
//     {runID, PID, processStartTime, BootID, device} marker atomically before
//     it opens the device, so a spawn whose exec broke afterwards is found
//     rather than duplicated, and a second start adopts the running writer —
//     but only when the marker names the very device this workload was pointed
//     at; anything else is an error instead of a silent adoption. A marker
//     whose process is provably gone (the node rebooted, or the pid was
//     reused) is cleared together with its journal, so the run id can be
//     started again.
//   - Run ids do not collide by accident: opts.RunID defaults to a name unique
//     to this call, so adoption only ever happens where a spec asked for it by
//     reusing a run id explicitly.
//   - Cleanup is registered before the spawn and is idempotent: it stops the
//     writer, verifies the last journal record, and only then removes the
//     writer's files. Because Ginkgo runs cleanups in reverse order, it always
//     runs before the teardown of the RVA that provided the device. Handles
//     that share a run id share one writer, so the first cleanup ends it and
//     the others find nothing left to verify.
//
// The writer needs python3 on the node; its absence fails the spec with an
// explicit message rather than silently skipping the I/O coverage.
func (f *Framework) StartIOWorkload(ctx context.Context, opts IOWorkloadOptions) *IOWorkload {
	GinkgoHelper()
	// The guard runs before the options are validated or defaulted, so the
	// operation is named from opts exactly as the caller passed them. The values
	// are quoted rather than interpolated bare: a field the caller left empty has
	// to be visible as "" instead of as a gap in the middle of the sentence.
	RequireDisruptiveSpec(fmt.Sprintf(
		"writing to the raw block device %q on node %q", opts.DevicePath, opts.NodeName))

	w, err := f.newIOWorkload(opts)
	if err != nil {
		Fail(fmt.Sprintf("io workload: %v", err))
	}

	// Registered before the spawn: from here on, no failure path can leave a
	// process writing to the device.
	DeferCleanup(func(ctx SpecContext) { w.Cleanup(ctx) })

	if err := w.start(ctx); err != nil {
		Fail(fmt.Sprintf("io workload %q on node %q: %v", w.opts.RunID, w.opts.NodeName, err))
	}
	return w
}

// newIOWorkload validates the options and applies the defaults.
func (f *Framework) newIOWorkload(opts IOWorkloadOptions) (*IOWorkload, error) {
	if opts.RunID == "" {
		// One name per call, not per spec: a suffixed UniqueName is stable
		// within a spec, so a second workload would silently address the first
		// one's marker and adopt its writer instead of starting its own.
		opts.RunID = f.UniqueName() + "-io"
	}

	switch {
	case opts.NodeName == "":
		return nil, errors.New("require: NodeName must not be empty")
	case opts.DevicePath == "":
		return nil, errors.New("require: DevicePath must not be empty (is the RVA attached?)")
	case !devicePathRe.MatchString(opts.DevicePath):
		return nil, fmt.Errorf("require: DevicePath %q is not a plain /dev path", opts.DevicePath)
	case opts.DRBDResourceName == "":
		return nil, errors.New("require: DRBDResourceName must not be empty")
	case !runIDRe.MatchString(opts.RunID):
		return nil, fmt.Errorf("require: RunID %q must match %s", opts.RunID, runIDRe)
	}

	if opts.Slots == 0 {
		opts.Slots = ioWorkloadDefaultSlots
	}
	if opts.SlotSize == 0 {
		opts.SlotSize = ioWorkloadDefaultSlotSize
	}
	if opts.Interval == 0 {
		opts.Interval = ioWorkloadDefaultInterval
	}
	if opts.MaxHeartbeatGap == 0 {
		opts.MaxHeartbeatGap = ioWorkloadDefaultMaxGap
	}
	if opts.StartTimeout == 0 {
		opts.StartTimeout = ioWorkloadDefaultStartTimeout
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = ioWorkloadDefaultStopTimeout
	}

	switch {
	case opts.Slots < 2:
		return nil, fmt.Errorf("require: Slots must be at least 2, got %d", opts.Slots)
	case opts.SlotSize < 512 || opts.SlotSize%512 != 0:
		return nil, fmt.Errorf("require: SlotSize must be a positive multiple of 512, got %d", opts.SlotSize)
	case opts.Interval <= 0:
		return nil, fmt.Errorf("require: Interval must be positive, got %s", opts.Interval)
	}

	return &IOWorkload{f: f, opts: opts, poll: ioWorkloadDefaultPoll}, nil
}

// RunID identifies this writer on the node.
func (w *IOWorkload) RunID() string { return w.opts.RunID }

// NodeName is the node running the writer.
func (w *IOWorkload) NodeName() string { return w.opts.NodeName }

// DevicePath is the device the writer was pointed at.
func (w *IOWorkload) DevicePath() string { return w.opts.DevicePath }

// JournalPath is the heartbeat journal on the node, useful in failure reports.
func (w *IOWorkload) JournalPath() string { return w.journalPath() }

// Observe returns the current status of the writer.
func (w *IOWorkload) Observe(ctx context.Context) IOWorkloadStatus {
	GinkgoHelper()
	st, err := w.observe(ctx)
	if err != nil {
		Fail(fmt.Sprintf("io workload %q on node %q: %v", w.opts.RunID, w.opts.NodeName, err))
	}
	return st
}

// AwaitProgress blocks until the writer completed minWrites more verified
// writes than it had at the moment of the call, and returns the status that
// satisfied it. It fails the spec when the writer terminates or stops making
// progress within StartTimeout.
func (w *IOWorkload) AwaitProgress(ctx context.Context, minWrites int64) IOWorkloadStatus {
	GinkgoHelper()
	st, err := w.awaitProgress(ctx, minWrites)
	if err != nil {
		Fail(fmt.Sprintf("io workload %q on node %q: %v", w.opts.RunID, w.opts.NodeName, err))
	}
	return st
}

// Stop asks the writer to finish and waits for its last record. It is
// idempotent and safe when the process is already gone.
func (w *IOWorkload) Stop(ctx context.Context) {
	GinkgoHelper()
	if err := w.stop(ctx); err != nil {
		Fail(fmt.Sprintf("io workload %q on node %q: %v", w.opts.RunID, w.opts.NodeName, err))
	}
}

// Cleanup stops the writer, verifies its last journal record and removes its
// files from the node. It is registered automatically by StartIOWorkload and
// is idempotent, so calling it explicitly is allowed.
func (w *IOWorkload) Cleanup(ctx context.Context) {
	GinkgoHelper()
	if err := w.cleanup(ctx); err != nil {
		Fail(fmt.Sprintf("io workload %q cleanup on node %q: %v", w.opts.RunID, w.opts.NodeName, err))
	}
}

// ---------------------------------------------------------------------------
// Core: everything below returns errors so it can be unit-tested with a stub
// runner, without a cluster.
// ---------------------------------------------------------------------------

// start brings the writer up: identity first, then an idempotent spawn, then
// the wait for proof that the data path works.
func (w *IOWorkload) start(ctx context.Context) error {
	minor, err := w.resolveExpectedMinor(ctx)
	if err != nil {
		return err
	}
	w.minor = minor

	if err := w.validateDevicePath(ctx, minor); err != nil {
		return err
	}

	st, err := w.observe(ctx)
	if err != nil {
		return err
	}
	switch {
	case st.Running:
		if err := w.checkAdoptedDevice(st); err != nil {
			return err
		}
		fmt.Fprintf(GinkgoWriter, "[%s] [io-workload] run=%s adopting the writer already running on node %s\n",
			time.Now().Format("15:04:05.000"), w.opts.RunID, w.opts.NodeName)
	default:
		// A marker that does not match means our writer is provably gone (the
		// node rebooted, or the pid was reused). The writer on the node refuses
		// to start while a marker exists, so it has to be cleared here — and
		// only here, where the process behind it is known to be dead.
		if st.probe.Marker != nil {
			fmt.Fprintf(GinkgoWriter, "[%s] [io-workload] run=%s clearing a stale marker on node %s: %s\n",
				time.Now().Format("15:04:05.000"), w.opts.RunID, w.opts.NodeName, st.Note)
			if err := w.clearStaleMarker(ctx); err != nil {
				return err
			}
		}
		if err := w.spawnUntilRunning(ctx); err != nil {
			return err
		}
	}

	st, err = w.await(ctx, w.opts.StartTimeout, "the first verified write", func(st IOWorkloadStatus) (bool, error) {
		if st.Terminated != nil {
			return false, fmt.Errorf("the writer terminated before the first verified write: %s", st.Terminated.Message)
		}
		return st.LastSequence >= 0, nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [io-workload] run=%s writing to %s (minor %d) on node %s: %s\n",
		time.Now().Format("15:04:05.000"), w.opts.RunID, w.opts.DevicePath, w.minor, w.opts.NodeName, st)
	return nil
}

// checkAdoptedDevice refuses to adopt a writer that was pointed at another
// device.
//
// Adoption hands this handle a process it did not start, and everything the
// handle later reports — progress, stalls, the final verdict — is then about
// that process. The device it writes to is therefore the one thing that must
// match: a run id reused for a different volume would otherwise turn another
// volume's I/O into evidence about this one. st.Running implies a marker that
// matched this run id, so the marker is non-nil here.
func (w *IOWorkload) checkAdoptedDevice(st IOWorkloadStatus) error {
	if device := st.probe.Marker.Device; device != w.opts.DevicePath {
		return fmt.Errorf(
			"run id %q is held by a writer of device %q on node %q, but this workload writes to %q: refusing to adopt it",
			w.opts.RunID, device, w.opts.NodeName, w.opts.DevicePath)
	}
	return nil
}

// resolveExpectedMinor asks the node's kernel which minor the resource owns.
func (w *IOWorkload) resolveExpectedMinor(ctx context.Context) (int, error) {
	res, err := w.f.runner().DrbdsetupRun(ctx, w.opts.NodeName, "status", "--json", w.opts.DRBDResourceName)
	if err != nil {
		return 0, fmt.Errorf("asking drbdsetup for the minor of %q: %w", w.opts.DRBDResourceName, err)
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("drbdsetup status %q exited with code %d: %s",
			w.opts.DRBDResourceName, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return parseDRBDMinorFromStatus(res.Stdout, w.opts.DRBDResourceName)
}

// validateDevicePath resolves the device path and refuses anything that is not
// the canonical DRBD device of this very resource. This runs before the device
// is opened; the writer additionally verifies identity on the open descriptor,
// which is what closes the swap window between this check and the first write.
func (w *IOWorkload) validateDevicePath(ctx context.Context, expectedMinor int) error {
	res, err := w.f.runner().HostRun(ctx, w.opts.NodeName,
		[]string{"readlink", "-f", w.opts.DevicePath}, "readlink -f "+w.opts.DevicePath)
	if err != nil {
		return fmt.Errorf("resolving device path %q: %w", w.opts.DevicePath, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("resolving device path %q exited with code %d: %s",
			w.opts.DevicePath, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	canonical := strings.TrimSpace(res.Stdout)
	minor, err := parseDRBDDevicePath(canonical)
	if err != nil {
		return fmt.Errorf("device path %q on node %q: %w", w.opts.DevicePath, w.opts.NodeName, err)
	}
	if minor != expectedMinor {
		return fmt.Errorf("device path %q resolves to %s (minor %d), but drbdsetup reports minor %d for resource %q: refusing to write to another volume's device",
			w.opts.DevicePath, canonical, minor, expectedMinor, w.opts.DRBDResourceName)
	}
	return nil
}

// clearStaleMarker removes the marker (and journal) of a writer that is no
// longer alive. Callers MUST have observed a non-matching marker first.
func (w *IOWorkload) clearStaleMarker(ctx context.Context) error {
	res, err := w.f.runner().HostRun(ctx, w.opts.NodeName,
		w.clearMarkerCommand(), "io-workload clear-stale-marker "+w.opts.RunID)
	if err != nil {
		return fmt.Errorf("clearing the stale marker: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("clearing the stale marker exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// spawnUntilRunning starts the writer, retrying only while no writer exists.
//
// The spawn exec is executed without the transport retry and every attempt is
// followed by a probe by run id, because a transport error can be reported
// after the writer was already forked: retrying blindly could add a second
// writer, and giving up blindly would lose the first one.
func (w *IOWorkload) spawnUntilRunning(ctx context.Context) error {
	var lastErr error

	for attempt := 1; attempt <= ioWorkloadSpawnAttempts; attempt++ {
		res, execErr := w.f.runner().HostRunNoRetry(ctx, w.opts.NodeName,
			w.spawnCommand(), "io-workload spawn "+w.opts.RunID)

		st, obsErr := w.observe(ctx)
		if obsErr != nil {
			return obsErr
		}
		if st.Running {
			// A writer for this run id exists; never spawn a second one. It is
			// ours by construction — the spawn just delivered the program with
			// our device — but the check costs nothing and closes the window
			// against a foreign writer that took the run id meanwhile.
			return w.checkAdoptedDevice(st)
		}
		if st.Terminated != nil {
			return nil // the writer of this run id already had its last word
		}

		if execErr == nil {
			// The command ran to completion, so its verdict is final.
			switch {
			case res.ExitCode != 0:
				return fmt.Errorf("spawning the writer exited with code %d: %s",
					res.ExitCode, strings.TrimSpace(res.Stderr))
			case !strings.Contains(res.Stdout, spawnedMarker):
				return fmt.Errorf("spawning the writer did not report %s: %s",
					spawnedMarker, truncate(strings.TrimSpace(res.Stdout), 256))
			}
			return nil
		}

		lastErr = fmt.Errorf("attempt %d: %w", attempt, execErr)
	}

	return fmt.Errorf("spawning the writer on node %q: %w", w.opts.NodeName, lastErr)
}

// observe runs one probe over the regular journal tail.
func (w *IOWorkload) observe(ctx context.Context) (IOWorkloadStatus, error) {
	return w.observeTail(ctx, ioWorkloadJournalTail)
}

// observeTail runs one probe over the given journal tail and turns it into a
// status.
func (w *IOWorkload) observeTail(ctx context.Context, tailLines int) (IOWorkloadStatus, error) {
	res, err := w.f.runner().HostRun(ctx, w.opts.NodeName,
		w.probeCommand(tailLines), "io-workload probe "+w.opts.RunID)
	if err != nil {
		return IOWorkloadStatus{}, fmt.Errorf("probing the writer: %w", err)
	}
	if res.ExitCode != 0 {
		return IOWorkloadStatus{}, fmt.Errorf("probing the writer exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	probe, err := parseIOProbe(res.Stdout)
	if err != nil {
		return IOWorkloadStatus{}, err
	}
	return w.statusFrom(probe), nil
}

// statusFrom derives the status from a probe.
func (w *IOWorkload) statusFrom(p ioProbe) IOWorkloadStatus {
	st := IOWorkloadStatus{LastSequence: -1, probe: p}
	st.Running, st.Note = matchIOMarker(p.Marker, w.opts.RunID, p.BootID, p.ProcStat)

	if beat := p.Journal.last(); beat != nil {
		st.LastSequence = beat.Sequence
		st.LastWriteAt = beat.At
		st.Gap = p.Now.Sub(beat.At)
		st.Stalled = p.Journal.Termination == nil && st.Gap > w.opts.MaxHeartbeatGap
	}
	if gap, endedBy := p.Journal.maxInterBeatGap(); endedBy != nil {
		st.MaxObservedGap = gap
		st.GapExceeded = gap > w.opts.MaxHeartbeatGap
	}
	if t := p.Journal.Termination; t != nil {
		st.Terminated = &IOWorkloadTermination{Failed: t.Failed, At: t.At, Message: t.Message}
	}
	return st
}

// awaitProgress waits for minWrites more verified writes.
func (w *IOWorkload) awaitProgress(ctx context.Context, minWrites int64) (IOWorkloadStatus, error) {
	from, err := w.observe(ctx)
	if err != nil {
		return IOWorkloadStatus{}, err
	}
	target := from.LastSequence + minWrites

	return w.await(ctx, w.opts.StartTimeout,
		fmt.Sprintf("%d more verified writes (sequence %d)", minWrites, target),
		func(st IOWorkloadStatus) (bool, error) {
			if st.Terminated != nil && st.Terminated.Failed {
				return false, fmt.Errorf("the writer failed: %s", st.Terminated.Message)
			}
			// A stall boundary visible in the tail is final evidence: the writer
			// stopped for longer than tolerated, no later progress undoes that.
			if st.GapExceeded {
				return false, fmt.Errorf("the writer stalled for %s (tolerated max %s): %s",
					st.MaxObservedGap.Truncate(time.Millisecond), w.opts.MaxHeartbeatGap, st)
			}
			return st.LastSequence >= target, nil
		})
}

// await polls the node until done is satisfied, the writer breaks the
// expectation, or the budget runs out.
func (w *IOWorkload) await(
	ctx context.Context,
	timeout time.Duration,
	what string,
	done func(IOWorkloadStatus) (bool, error),
) (IOWorkloadStatus, error) {
	deadline := time.Now().Add(timeout)
	var last IOWorkloadStatus

	for {
		st, err := w.observe(ctx)
		if err != nil {
			return last, err
		}
		last = st

		ok, err := done(st)
		if err != nil {
			return last, err
		}
		if ok {
			return last, nil
		}
		if !time.Now().Before(deadline) {
			return last, fmt.Errorf("timed out after %s waiting for %s; last status: %s", timeout, what, last)
		}

		select {
		case <-ctx.Done():
			return last, fmt.Errorf("waiting for %s: %w; last status: %s", what, ctx.Err(), last)
		case <-time.After(w.poll):
		}
	}
}

// stop signals the writer and waits for its last record.
func (w *IOWorkload) stop(ctx context.Context) error {
	st, err := w.observe(ctx)
	if err != nil {
		return err
	}
	if !st.Running {
		return nil // idempotent: nothing of ours is running
	}

	if err := w.signal(ctx, "TERM", st); err != nil {
		return err
	}

	_, err = w.await(ctx, w.opts.StopTimeout, "the writer to stop", func(st IOWorkloadStatus) (bool, error) {
		return !st.Running || st.Terminated != nil, nil
	})
	return err
}

// signal sends sig to the writer of this run — and only to it. A marker that
// does not match (different boot id, or a start time that says the pid was
// reused) means the process is not ours, and nothing is signalled.
func (w *IOWorkload) signal(ctx context.Context, sig string, st IOWorkloadStatus) error {
	if !st.Running || st.probe.Marker == nil {
		fmt.Fprintf(GinkgoWriter, "[%s] [io-workload] run=%s not signalling %s on node %s: %s\n",
			time.Now().Format("15:04:05.000"), w.opts.RunID, sig, w.opts.NodeName, st.Note)
		return nil
	}

	res, err := w.f.runner().HostRun(ctx, w.opts.NodeName,
		w.signalCommand(sig, st.probe.Marker.ProcStartTime), "io-workload signal "+sig+" "+w.opts.RunID)
	if err != nil {
		return fmt.Errorf("signalling %s to the writer: %w", sig, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("signalling %s to the writer exited with code %d: %s",
			sig, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// cleanup stops the writer, checks its last record while the journal is still
// there, and only then removes the writer's files.
//
// It is idempotent: once the node has been purged there is nothing left to
// inspect, and a repeated call must not turn the missing journal into a
// failure. The same holds across handles — two handles sharing a run id share
// one writer, and whichever cleans up first takes the journal with it.
func (w *IOWorkload) cleanup(ctx context.Context) error {
	if w.cleanedUp {
		return nil
	}
	w.cleanedUp = true

	stopErr := w.stop(ctx)

	// The final observation reads the WHOLE journal, not the regular tail:
	// this is where a stall anywhere in the run — even one that ended long
	// before the last probe — becomes a verdict.
	final, obsErr := w.observeTail(ctx, ioWorkloadJournalFull)
	var verifyErr error
	if obsErr == nil {
		verifyErr = w.verifyFinal(final)

		// A writer that ignored SIGTERM must not outlive the spec.
		if final.Running {
			if err := w.signal(ctx, "KILL", final); err != nil {
				verifyErr = errors.Join(verifyErr, err)
			}
		}
	}

	return errors.Join(stopErr, obsErr, verifyErr, w.purge(ctx))
}

// verifyFinal is the last continuity check: the workload must have written
// something, must not have ended on an I/O or identity failure, and must not
// have stalled beyond MaxHeartbeatGap at any point of the run. The caller
// hands it a full-journal observation, so the stall check covers the whole
// run, not just the last probe's tail.
func (w *IOWorkload) verifyFinal(st IOWorkloadStatus) error {
	switch {
	case st.Terminated != nil && st.Terminated.Failed:
		return fmt.Errorf("the writer failed: %s (journal: %s)", st.Terminated.Message, w.journalPath())
	case st.probe.isEmpty():
		// The node holds no trace of this run: an earlier cleanup — this
		// handle's, or that of another handle sharing the run id — already
		// removed the journal. There is nothing left to verify, and a verdict
		// invented from the absence of evidence would only break idempotency.
		return nil
	case st.LastSequence < 0:
		return fmt.Errorf("the writer completed no verified write (journal: %s)", w.journalPath())
	case st.GapExceeded:
		return fmt.Errorf("the writer stalled for %s during the run (tolerated max %s; journal: %s)",
			st.MaxObservedGap.Truncate(time.Millisecond), w.opts.MaxHeartbeatGap, w.journalPath())
	}
	return nil
}

// purge removes the marker, journal and program of this run from the node.
func (w *IOWorkload) purge(ctx context.Context) error {
	res, err := w.f.runner().HostRun(ctx, w.opts.NodeName, w.purgeCommand(), "io-workload purge "+w.opts.RunID)
	if err != nil {
		return fmt.Errorf("removing the writer's files: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("removing the writer's files exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
