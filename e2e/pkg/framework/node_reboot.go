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
	. "github.com/onsi/gomega"
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
	nodeName     string
	bootIDBefore string
	issuedAt     time.Time
}

// RebootNode reboots the host of nodeName and returns a handle for awaiting
// the node's return. It is used only by Disruptive specs to simulate a node
// outage.
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

	bootIDBefore := f.NodeBootID(ctx, nodeName)
	Expect(bootIDBefore).NotTo(BeEmpty(), "reboot: node %q reports no boot ID", nodeName)

	// Warm the pod-name cache through the retrying path first: the reboot
	// itself runs once, and a transport error caused by a stale cached pod
	// would be indistinguishable from a reboot that never started.
	if _, err := f.runner().HostRun(ctx, nodeName, []string{"true"}, "reboot preflight"); err != nil {
		Fail(fmt.Sprintf("reboot: preflight exec on node %q failed: %v", nodeName, err))
	}

	if err := f.rebootNodeExec(ctx, nodeName); err != nil {
		Fail(fmt.Sprintf("reboot: node %q: %v", nodeName, err))
	}

	return &NodeReboot{f: f, nodeName: nodeName, bootIDBefore: bootIDBefore, issuedAt: time.Now()}
}

// AwaitCompleted blocks until the reboot completed: the node's boot ID changed
// AND the node is currently Ready.
//
// Observing Ready=False in between is a progress signal only — a fast reboot
// may never be published as NotReady, so requiring that observation would make
// specs flaky.
func (r *NodeReboot) AwaitCompleted(ctx context.Context) {
	GinkgoHelper()

	sawNotReady := false
	Eventually(ctx, func() error {
		node, err := r.f.getNode(ctx, r.nodeName)
		if err != nil {
			return err
		}
		bootID := node.Status.NodeInfo.BootID
		ready := isNodeReady(node)
		if !ready && !sawNotReady {
			sawNotReady = true
			fmt.Fprintf(GinkgoWriter, "[%s] [reboot] node=%s observed NotReady after %s\n",
				time.Now().Format("15:04:05.000"), r.nodeName, time.Since(r.issuedAt).Truncate(time.Second))
		}
		if bootID == r.bootIDBefore {
			return fmt.Errorf("node %q still reports boot ID %q", r.nodeName, bootID)
		}
		if !ready {
			return fmt.Errorf("node %q rebooted (boot ID %q) but is not Ready yet", r.nodeName, bootID)
		}
		return nil
	}).WithTimeout(rebootCompletionTimeout).WithPolling(rebootCompletionPoll).Should(Succeed(),
		"node %q did not come back from the reboot", r.nodeName)

	fmt.Fprintf(GinkgoWriter, "[%s] [reboot] node=%s back and Ready after %s (sawNotReady=%t)\n",
		time.Now().Format("15:04:05.000"), r.nodeName, time.Since(r.issuedAt).Truncate(time.Second), sawNotReady)
}

// NodeBootID returns status.nodeInfo.bootID of the node. It fails the spec when
// the node cannot be read.
func (f *Framework) NodeBootID(ctx context.Context, nodeName string) string {
	GinkgoHelper()
	node, err := f.getNode(ctx, nodeName)
	Expect(err).NotTo(HaveOccurred(), "reading node %q", nodeName)
	return node.Status.NodeInfo.BootID
}

// rebootNodeExec issues exactly one reboot command and classifies its outcome.
func (f *Framework) rebootNodeExec(ctx context.Context, nodeName string) error {
	res, err := f.runner().HostRunNoRetry(ctx, nodeName, rebootCommand(), "systemctl reboot")
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
