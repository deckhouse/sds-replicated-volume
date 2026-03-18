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
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/utils/exec"

	dbg "github.com/deckhouse/sds-replicated-volume/e2e/pkg/debug"
)

// ExecResult holds the outcome of a command executed inside a Kubernetes pod.
// The caller is responsible for asserting ExitCode, Stdout, and Stderr
// using gomega matchers — the framework never fails the test on non-zero
// exit codes.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// podTarget identifies a DaemonSet-like workload where exactly one pod
// runs per node. Used by execOnNode to discover the right pod.
type podTarget struct {
	namespace     string
	labelSelector string
	container     string
}

var (
	agentTarget = podTarget{
		namespace:     "d8-sds-replicated-volume",
		labelSelector: "app=agent",
		container:     "agent",
	}
	sncTarget = podTarget{
		namespace:     "d8-sds-node-configurator",
		labelSelector: "app=sds-node-configurator",
		container:     "sds-node-configurator-agent",
	}
)

const (
	nsenterBin = "/opt/deckhouse/sds/bin/nsenter.static"
	lvmBin     = "/opt/deckhouse/sds/bin/lvm.static"
)

// Drbdsetup executes `drbdsetup <args>` inside the agent pod running on
// nodeName and returns the result. The test is not failed on non-zero
// exit codes — use gomega matchers on ExecResult.
func (f *Framework) Drbdsetup(ctx context.Context, nodeName string, args ...string) ExecResult {
	cmd := append([]string{"drbdsetup"}, args...)
	return f.execOnNode(ctx, agentTarget, nodeName, cmd, "drbdsetup "+strings.Join(args, " "))
}

// LVM executes `lvm.static <args>` on the host of nodeName via nsenter
// inside the sds-node-configurator pod and returns the result.
func (f *Framework) LVM(ctx context.Context, nodeName string, args ...string) ExecResult {
	cmd := []string{nsenterBin, "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", lvmBin}
	cmd = append(cmd, args...)
	return f.execOnNode(ctx, sncTarget, nodeName, cmd, "lvm "+strings.Join(args, " "))
}

// execOnNode discovers the pod matching target on nodeName, executes cmd
// inside it via SPDY, logs everything to GinkgoWriter, and returns the result.
// displayCmd is the human-readable command string used in log output (e.g.
// "drbdsetup status myres" instead of the raw cmd slice).
func (f *Framework) execOnNode(ctx context.Context, target podTarget, nodeName string, cmd []string, displayCmd string) ExecResult {
	podName := f.findPodOnNode(ctx, target, nodeName)

	req := f.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(target.namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: target.container,
		Command:   cmd,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(f.restConfig, "POST", req.URL())
	if err != nil {
		Fail(fmt.Sprintf("creating SPDY executor for pod %q on node %q: %v", podName, nodeName, err))
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [exec] node=%s $ %s\n",
		time.Now().Format("15:04:05.000"), nodeName, displayCmd)

	var stdout, stderr, combined bytes.Buffer
	stderrColored := &colorWriter{
		inner: io.MultiWriter(&stderr, &combined),
		color: dbg.ColorRed,
		reset: dbg.ColorReset,
	}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: io.MultiWriter(&stdout, &combined),
		Stderr: stderrColored,
	})

	result := ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(utilexec.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			Fail(fmt.Sprintf("exec in pod %q on node %q (cmd: %s): %v\nstdout: %s\nstderr: %s",
				podName, nodeName, strings.Join(cmd, " "), err, result.Stdout, result.Stderr))
		}
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [exec] node=%s $ %s -> exit=%d\n",
		time.Now().Format("15:04:05.000"), nodeName, displayCmd, result.ExitCode)
	if combined.Len() > 0 {
		fmt.Fprint(GinkgoWriter, combined.String())
		if !strings.HasSuffix(combined.String(), "\n") {
			fmt.Fprintln(GinkgoWriter)
		}
	}

	return result
}

// colorWriter wraps each Write in ANSI color codes. When color is empty
// (NO_COLOR), it passes data through unchanged.
type colorWriter struct {
	inner io.Writer
	color string
	reset string
}

func (w *colorWriter) Write(p []byte) (int, error) {
	if w.color == "" {
		return w.inner.Write(p)
	}
	_, err := fmt.Fprintf(w.inner, "%s%s%s", w.color, p, w.reset)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// findPodOnNode lists pods matching the target's label selector and node
// field selector, expecting exactly one match. Fails the test otherwise.
func (f *Framework) findPodOnNode(ctx context.Context, target podTarget, nodeName string) string {
	pods, err := f.clientset.CoreV1().Pods(target.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: target.labelSelector,
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		Fail(fmt.Sprintf("listing pods (label=%s) on node %q in namespace %s: %v",
			target.labelSelector, nodeName, target.namespace, err))
	}
	if len(pods.Items) != 1 {
		Fail(fmt.Sprintf("expected 1 pod (label=%s) on node %q in namespace %s, got %d",
			target.labelSelector, nodeName, target.namespace, len(pods.Items)))
	}
	return pods.Items[0].Name
}
