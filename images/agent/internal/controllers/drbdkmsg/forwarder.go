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

package drbdkmsg

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"
	"unsafe"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/deckhouse/sds-replicated-volume/lib/go/common/kmsg"
)

// Look for messages starting with "drbd"
var drbdMatch = []byte(";drbd")

// CacheInvalidator is the subset of the drbdsetup status/show cache that the
// forwarder drives. State-change kernel messages invalidate a single resource;
// messages that match the state-change filter but whose resource name cannot be
// parsed fall back to invalidating everything.
type CacheInvalidator interface {
	Invalidate(resourceName string)
	InvalidateAll()
}

// kmsgResourcePrefix precedes the DRBD resource name in every /dev/kmsg record
// emitted by the DRBD kernel module: "<hdr>;drbd <name>[/vol drbdN] [peer]: ...".
var kmsgResourcePrefix = []byte(";drbd ")

// unregisteredPrefix is inserted between "drbd " and the resource name while a
// resource/connection is being torn down (see the kernel's polymorph printk).
var unregisteredPrefix = []byte("/unregistered/")

// stateChangeMarkers are substrings identifying a DRBD kernel message that
// reports a state transition changing what drbdsetup status/show return, so the
// affected resource's cached output must be dropped. They are the transition
// tags emitted by the kernel's print_state_change plus split-brain detection.
var stateChangeMarkers = [][]byte{
	[]byte("role( "),
	[]byte("susp-io( "),
	[]byte("force-io-failures( "),
	[]byte("conn( "),
	[]byte("peer( "),
	[]byte("disk( "),
	[]byte("quorum( "),
	[]byte("pdsk( "),
	[]byte("repl( "),
	[]byte("resync-susp( "),
	[]byte("Split-Brain"),
}

// isStateChangeMessage reports whether line is a DRBD state-transition message
// whose occurrence means the cached status/show output is stale.
func isStateChangeMessage(line []byte) bool {
	for _, m := range stateChangeMarkers {
		if bytes.Contains(line, m) {
			return true
		}
	}
	return false
}

// parseResourceName extracts the DRBD resource name from a /dev/kmsg record.
// The name is the token after "drbd " (minus an optional "/unregistered/"
// prefix), terminated by a space, '/', or ':'. Returns false when the record
// carries no resource name (e.g. module-level "drbd:" messages).
//
// The returned string is a zero-copy view into line and is valid only until the
// next kmsg read; callers must not retain it.
func parseResourceName(line []byte) (string, bool) {
	_, after, ok := bytes.Cut(line, kmsgResourcePrefix)
	if !ok {
		return "", false
	}
	rest := bytes.TrimPrefix(after, unregisteredPrefix)
	end := bytes.IndexAny(rest, " /:")
	if end <= 0 {
		return "", false
	}
	name := rest[:end]
	return unsafe.String(unsafe.SliceData(name), len(name)), true
}

// benignKernelMessages are substrings of DRBD kernel messages that the kernel
// emits at warning/error severity but which are expected and harmless (normal
// teardown / disconnect chatter). They are forwarded at Info instead of Error
// so they do not look like real failures:
//
//   - "Can not disconnect a StandAlone device": during peer removal the agent
//     explicitly disconnects a peer before del-peer; del-peer then performs its
//     own implicit disconnect, which fails because the connection is already
//     StandAlone. The peer is still removed successfully — only the redundant
//     state change is rejected.
//   - "meta connection shut down by peer": a peer closed its connection, which
//     is expected when a peer resource is downed/removed during teardown.
var benignKernelMessages = [][]byte{
	[]byte("Can not disconnect a StandAlone device"),
	[]byte("meta connection shut down by peer"),
}

// isBenignKernelMessage reports whether line contains any known-benign DRBD
// kernel message substring (see benignKernelMessages).
func isBenignKernelMessage(line []byte) bool {
	for _, m := range benignKernelMessages {
		if bytes.Contains(line, m) {
			return true
		}
	}
	return false
}

var (
	retryBaseDelay = 10 * time.Millisecond
	retryMaxDelay  = 10 * time.Second
)

// Kernel printk severities (kernel.h / RFC 5424). The bottom 3 bits of
// the wire-format <pri> field carry the severity; the upper bits are
// the facility, which we ignore.
const (
	kernSeverityWarning = 4
	kernSeverityDebug   = 7
)

// Forwarder is a manager.Runnable that follows /dev/kmsg and emits
// DRBD-tagged kernel messages through the controller-runtime logger. It also
// serves as a secondary cache-invalidation signal: state-transition messages
// drop the affected resource's cached drbdsetup output.
type Forwarder struct {
	// inv receives cache-invalidation calls for state-change kernel
	// messages. May be nil, in which case invalidation is skipped.
	inv CacheInvalidator

	// retryDelay is the current backoff between failed kmsg.Open
	// attempts and follow restarts. It doubles up to retryMaxDelay
	// after each failure and is reset to retryBaseDelay every time
	// follow successfully reads a record.
	retryDelay time.Duration
}

func NewForwarder(inv CacheInvalidator) *Forwarder {
	return &Forwarder{inv: inv}
}

// invalidateForKernelMessage drops cached status/show output when line reports
// a DRBD state transition (see isStateChangeMessage). It invalidates just the
// named resource, falling back to InvalidateAll when the name cannot be parsed.
func (f *Forwarder) invalidateForKernelMessage(logger logr.Logger, line []byte) {
	if f.inv == nil || !isStateChangeMessage(line) {
		return
	}
	if name, ok := parseResourceName(line); ok {
		logger.V(1).Info("invalidating DRBD caches for kernel state change", "resource", name)
		f.inv.Invalidate(name)
		return
	}
	logger.V(1).Info("invalidating all DRBD caches for unparseable kernel state change")
	f.inv.InvalidateAll()
}

// Start runs the kmsg follow loop until ctx is canceled. It never
// returns a non-nil error: kmsg forwarding is observability, not a
// critical path, so any failure here is logged and retried instead of
// crashing the agent.
func (f *Forwarder) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName(ControllerName)
	logger.Info("starting DRBD kmsg forwarder")

	buf := make([]byte, kmsg.MaxRecordSize)
	f.retryDelay = retryBaseDelay
	for ctx.Err() == nil {
		err := f.follow(ctx, logger, buf)
		if ctx.Err() != nil {
			return nil
		}
		logger.Error(err, "kmsg follow error; retrying", "retryDelay", f.retryDelay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(f.retryDelay):
		}
		f.retryDelay = min(f.retryDelay*2, retryMaxDelay)
	}
	return nil
}

// NeedLeaderElection is false: /dev/kmsg is per-node, so every agent
// instance follows its own.
func (*Forwarder) NeedLeaderElection() bool {
	return false
}

// follow opens /dev/kmsg and reads records into buf until the watchdog
// closes the reader (ctx cancel) or an unrecoverable read error
// occurs. The watchdog goroutine is the sole closer of r: closing
// from anywhere else would race with a Read parked on the poller.
//
// Each successful Read resets f.retryDelay so a healthy stream of
// records collapses any prior backoff back to retryBaseDelay.
func (f *Forwarder) follow(ctx context.Context, logger logr.Logger, buf []byte) error {
	r, err := kmsg.Open()
	if err != nil {
		return err
	}

	// Wrap ctx so the watchdog wakes when follow returns for any
	// reason, not just on outer cancellation.
	ctx, cancel := context.WithCancel(ctx)
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		<-ctx.Done()
		_ = r.Close()
	}()
	defer func() {
		cancel()
		<-watchdogDone
	}()

	for {
		n, err := r.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}
		f.retryDelay = retryBaseDelay
		line := buf[:n]
		if !bytes.Contains(line, drbdMatch) {
			continue
		}
		sev := parseSeverity(line)
		line = bytes.TrimSpace(line)

		// Use the message as a secondary cache-invalidation signal before
		// forwarding it for observability.
		f.invalidateForKernelMessage(logger, line)

		const event = "DRBD kernel message"
		switch {
		case isBenignKernelMessage(line):
			// Known-benign teardown/disconnect chatter (see
			// benignKernelMessages). Forward at Info despite the kernel's
			// warning/error severity.
			logger.Info(event, "text", string(line))
		case sev >= 0 && sev <= kernSeverityWarning:
			// EMERG..WARNING: real problems and "something might be
			// wrong" (e.g. DRBD resync rate caps, bitmap pressure).
			// Surface as Error so SREs notice in kubectl logs.
			logger.Error(nil, event, "text", string(line))
		case sev == kernSeverityDebug:
			logger.V(1).Info(event, "text", string(line))
		default:
			// NOTICE, INFO, or unparseable header: routine.
			logger.Info(event, "text", string(line))
		}
	}
}

// parseSeverity extracts the syslog severity (0..7) from the leading
// <pri> field of a /dev/kmsg wire-format record without allocating.
// The format is "<pri>,<seq>,<ts>,<flags>;<message>"; <pri> is at most
// 3 ASCII digits and encodes facility<<3 | severity, so the severity
// is the low 3 bits. Returns -1 if the header is missing or malformed.
func parseSeverity(line []byte) int {
	var pri int
	for i := range line {
		b := line[i]
		if b == ',' {
			if i == 0 || i > 3 {
				return -1
			}
			return pri & 7
		}
		if b < '0' || b > '9' {
			return -1
		}
		pri = pri*10 + int(b-'0')
	}
	return -1
}
