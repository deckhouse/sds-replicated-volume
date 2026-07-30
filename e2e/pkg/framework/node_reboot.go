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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// rebootMarker is printed by the reboot command right before the host is told
// to go down. Seeing it is the only proof that the reboot actually started when
// the exec connection dies together with the node.
const rebootMarker = "REBOOT_STARTED"

const (
	rebootCompletionTimeout = 10 * time.Minute
	rebootCompletionPoll    = 5 * time.Second
)

// NodeReboot tracks a reboot issued by RebootNode. Completion is asserted
// separately (AwaitCompleted), so a spec can observe the outage while the node
// is down.
type NodeReboot struct {
	f            *Framework
	clock        rebootClock
	nodeName     string
	bootIDBefore string
	issuedAt     time.Time
}

// RebootNode reboots the host of nodeName and returns a handle for awaiting
// the node's return.
//
// The calling spec MUST carry LabelDisruptive, on itself or on an enclosing
// container: this takes a node of a shared cluster down. The requirement is
// enforced, not merely stated — RequireDisruptiveSpec fails the spec before
// anything is executed on the host.
//
// Protocol:
//   - The node's boot ID is recorded before anything is executed.
//   - The command prints the REBOOT_STARTED marker and runs sync before
//     `systemctl reboot`, all in one shell invocation.
//   - It is executed through a no-retry exec, because the retrying path would
//     re-run `systemctl reboot` against a freshly resolved pod on a transport
//     error.
//   - A non-zero exit always fails the spec; a transport error is accepted only
//     when the marker was already received; no marker means the reboot was not
//     proven to have started and the spec fails rather than silently waiting.
//
// RebootNode does NOT wait for the node to come back — call AwaitCompleted at
// the point where the spec expects recovery. Goroutine-safe.
func (f *Framework) RebootNode(ctx context.Context, nodeName string) *NodeReboot {
	GinkgoHelper()
	RequireDisruptiveSpec("rebooting node " + nodeName)

	clock := realClock{}
	bootIDBefore, err := issueReboot(ctx, f, f.runner(), nodeName)
	if err != nil {
		Fail(fmt.Sprintf("reboot: %v", err))
	}

	return &NodeReboot{
		f:            f,
		clock:        clock,
		nodeName:     nodeName,
		bootIDBefore: bootIDBefore,
		issuedAt:     clock.Now(),
	}
}

// AwaitCompleted blocks until the reboot completed: the node's boot ID changed
// AND the node is currently Ready.
//
// Observing Ready=False in between is a progress signal only — a fast reboot
// may never be published as NotReady, so requiring that observation would make
// specs flaky.
func (r *NodeReboot) AwaitCompleted(ctx context.Context) {
	GinkgoHelper()

	watch := &rebootWatch{nodeName: r.nodeName, bootIDBefore: r.bootIDBefore, issuedAt: r.issuedAt}
	if err := awaitRebootCompleted(ctx, r.f, r.clock, watch,
		rebootCompletionTimeout, rebootCompletionPoll); err != nil {
		Fail(err.Error())
	}
}

// NodeBootID returns status.nodeInfo.bootID of the node. It fails the spec when
// the node cannot be read.
func (f *Framework) NodeBootID(ctx context.Context, nodeName string) string {
	GinkgoHelper()
	bootID, err := nodeBootID(ctx, f, nodeName)
	if err != nil {
		Fail(err.Error())
	}
	return bootID
}

// ---------------------------------------------------------------------------
// Core
// ---------------------------------------------------------------------------

// nodeReader is the seam the reboot cores read Node objects through.
// *Framework reads them with the framework client; unit tests substitute a
// stub, so the reboot protocol is exercised without a cluster.
type nodeReader interface {
	getNode(ctx context.Context, nodeName string) (*corev1.Node, error)
}

// rebootClock is the time seam of the completion poll. The framework passes the
// real clock; unit tests pass one they drive, so the timeout and cancellation
// paths are covered without waiting out the real budget.
type rebootClock interface {
	Now() time.Time
	// Wait blocks for d, or until ctx ends — in which case it returns the
	// context's error.
	Wait(ctx context.Context, d time.Duration) error
}

// realClock is the rebootClock used against a real cluster.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// issueReboot is the failing logic of RebootNode: record the boot ID, warm the
// exec path, then issue exactly one reboot command. It returns the boot ID
// observed before the reboot — the value the completion poll watches.
func issueReboot(ctx context.Context, nodes nodeReader, runner nodeRunner, nodeName string) (string, error) {
	bootIDBefore, err := nodeBootID(ctx, nodes, nodeName)
	if err != nil {
		return "", err
	}
	if bootIDBefore == "" {
		return "", fmt.Errorf("node %q reports no boot ID", nodeName)
	}

	// Warm the pod-name cache through the retrying path first: the reboot
	// itself runs once, and a transport error caused by a stale cached pod
	// would be indistinguishable from a reboot that never started.
	if _, err := runner.HostRun(ctx, nodeName, []string{"true"}, "reboot preflight"); err != nil {
		return "", fmt.Errorf("preflight exec on node %q failed: %w", nodeName, err)
	}

	if err := rebootNodeExec(ctx, runner, nodeName); err != nil {
		return "", fmt.Errorf("node %q: %w", nodeName, err)
	}
	return bootIDBefore, nil
}

// rebootWatch is the state of one completion poll: what proves this reboot
// finished, and the progress observed so far.
type rebootWatch struct {
	nodeName     string
	bootIDBefore string
	issuedAt     time.Time
	sawNotReady  bool
}

// observe classifies one Node snapshot against the completion criterion: the
// boot ID changed AND the node is Ready right now. A nil error means the reboot
// completed; anything else says why it did not, yet.
//
// Ready=False is recorded as progress only — a fast reboot may never be
// published as NotReady, so requiring that observation would make specs flaky.
func (w *rebootWatch) observe(now time.Time, node *corev1.Node) error {
	bootID := node.Status.NodeInfo.BootID
	ready := isNodeReady(node)

	if !ready && !w.sawNotReady {
		w.sawNotReady = true
		fmt.Fprintf(GinkgoWriter, "[%s] [reboot] node=%s observed NotReady after %s\n",
			now.Format("15:04:05.000"), w.nodeName, now.Sub(w.issuedAt).Truncate(time.Second))
	}

	if bootID == w.bootIDBefore {
		return fmt.Errorf("node %q still reports boot ID %q", w.nodeName, bootID)
	}
	if !ready {
		return fmt.Errorf("node %q rebooted (boot ID %q) but is not Ready yet", w.nodeName, bootID)
	}
	return nil
}

// awaitRebootCompleted polls the node until the watch accepts it, the budget
// runs out, or the context ends.
//
// A failed read is not a verdict: the API server is reached through the very
// outage the spec is waiting out, so a read error counts as "not yet" and is
// only reported if the budget expires on it.
func awaitRebootCompleted(
	ctx context.Context,
	nodes nodeReader,
	clock rebootClock,
	watch *rebootWatch,
	timeout, poll time.Duration,
) error {
	deadline := clock.Now().Add(timeout)
	var last error

	for {
		node, err := nodes.getNode(ctx, watch.nodeName)
		if err != nil {
			last = fmt.Errorf("reading node %q: %w", watch.nodeName, err)
		} else {
			last = watch.observe(clock.Now(), node)
		}

		if last == nil {
			now := clock.Now()
			fmt.Fprintf(GinkgoWriter, "[%s] [reboot] node=%s back and Ready after %s (sawNotReady=%t)\n",
				now.Format("15:04:05.000"), watch.nodeName,
				now.Sub(watch.issuedAt).Truncate(time.Second), watch.sawNotReady)
			return nil
		}
		if !clock.Now().Before(deadline) {
			return fmt.Errorf("node %q did not come back from the reboot within %s: %w",
				watch.nodeName, timeout, last)
		}
		if err := clock.Wait(ctx, poll); err != nil {
			return fmt.Errorf("waiting for node %q to come back: %w; last state: %v",
				watch.nodeName, err, last)
		}
	}
}

// nodeBootID reads status.nodeInfo.bootID off the node.
func nodeBootID(ctx context.Context, nodes nodeReader, nodeName string) (string, error) {
	node, err := nodes.getNode(ctx, nodeName)
	if err != nil {
		return "", fmt.Errorf("reading node %q: %w", nodeName, err)
	}
	return node.Status.NodeInfo.BootID, nil
}

// rebootNodeExec issues exactly one reboot command and classifies its outcome.
func rebootNodeExec(ctx context.Context, runner nodeRunner, nodeName string) error {
	res, err := runner.HostRunNoRetry(ctx, nodeName, rebootCommand(), "systemctl reboot")
	return classifyRebootExec(res, err)
}

// rebootCommand returns the host command that announces the reboot and then
// performs it. The marker and the reboot MUST stay in the same invocation:
// a separate "announce" exec would prove nothing about the reboot exec.
func rebootCommand() []string {
	return []string{"sh", "-c", "printf '%s\\n' " + rebootMarker + "; sync; systemctl reboot"}
}

// classifyRebootExec decides whether the reboot was proven to have started.
//
//	exit != 0                -> failure, regardless of the marker
//	transport error + marker -> success (the connection died with the node)
//	transport error, no marker -> failure (the command may never have run)
//	exit 0, no marker        -> failure (something else answered)
func classifyRebootExec(res ExecResult, execErr error) error {
	markerSeen := strings.Contains(res.Stdout, rebootMarker)

	switch {
	case res.ExitCode != 0:
		return fmt.Errorf("reboot command exited with code %d (marker seen: %t); stdout: %q, stderr: %q",
			res.ExitCode, markerSeen, res.Stdout, res.Stderr)
	case markerSeen:
		return nil
	case execErr != nil:
		return fmt.Errorf("reboot command failed before printing %s, so it is unknown whether the node is rebooting: %w",
			rebootMarker, execErr)
	default:
		return fmt.Errorf("reboot command exited 0 without printing %s; stdout: %q, stderr: %q",
			rebootMarker, res.Stdout, res.Stderr)
	}
}

// getNode reads a Node object through the framework client.
func (f *Framework) getNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	var node corev1.Node
	if err := f.Client.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

// isNodeReady reports whether the node currently has Ready=True.
func isNodeReady(node *corev1.Node) bool {
	for i := range node.Status.Conditions {
		c := &node.Status.Conditions[i]
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
