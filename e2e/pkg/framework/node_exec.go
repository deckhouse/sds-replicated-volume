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

// podCacheKey is the lookup key for the pod-name cache in Framework.
type podCacheKey struct {
	target   podTarget
	nodeName string
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

// nsenterCandidates lists the host-entry binary paths that sds-node-configurator
// has shipped inside its pod. Newer builds dropped the ".static" suffix
// (nsenter.static -> nsenter); resolveNsenterBin probes these in order so the
// suite works against either node-configurator version.
var nsenterCandidates = []string{
	"/opt/deckhouse/sds/bin/nsenter",
	"/opt/deckhouse/sds/bin/nsenter.static",
}

// lvmCandidates lists the lvm binary paths sds-node-configurator has shipped
// inside its pod. Newer builds dropped the ".static" suffix (lvm.static ->
// lvm); resolveLvmBin probes these in order so the suite works against either
// node-configurator version. Mirrors nsenterCandidates.
var lvmCandidates = []string{
	"/opt/deckhouse/sds/bin/lvm",
	"/opt/deckhouse/sds/bin/lvm.static",
}

// nodeRunner is the seam through which framework helpers reach a node. The
// production implementation execs into pods (podRunner); helper unit tests
// substitute a stub via the Framework.nodeRun field, so helper logic can be
// exercised without a cluster.
type nodeRunner interface {
	// HostRun executes cmd in the host namespaces of nodeName. A transport
	// error against a cached pod is retried once with a fresh pod lookup, so
	// cmd MUST be safe to execute twice.
	HostRun(ctx context.Context, nodeName string, cmd []string, displayCmd string) (ExecResult, error)

	// HostRunNoRetry executes cmd in the host namespaces of nodeName exactly
	// once. Use it for commands that must never run twice. A non-nil error is
	// always a transport error (a non-zero exit code is reported in the
	// ExecResult), and the ExecResult still carries whatever output arrived
	// before the connection broke.
	HostRunNoRetry(ctx context.Context, nodeName string, cmd []string, displayCmd string) (ExecResult, error)

	// DrbdsetupRun executes `drbdsetup <args>` in the agent pod on nodeName.
	DrbdsetupRun(ctx context.Context, nodeName string, args ...string) (ExecResult, error)

	// PodRun executes cmd in container of the pod namespace/pod — any pod, in
	// any namespace, addressed by name instead of by node. A transport error is
	// retried once, so cmd MUST be safe to execute twice; that makes this the
	// path for reads (probing a journal, re-hashing a file) and PodRunNoRetry
	// the path for everything that changes the pod's state.
	//
	// Unlike the node methods there is no pod discovery and no pod-name cache:
	// the caller owns the pod it created and names it, so a stale cache entry —
	// the reason the node path retries at all — cannot exist here.
	PodRun(ctx context.Context, namespace, pod, container string, cmd []string, displayCmd string) (ExecResult, error)

	// PodRunNoRetry executes cmd in container of the pod namespace/pod exactly
	// once. Same semantics as HostRunNoRetry: a non-nil error is always a
	// transport error (a non-zero exit code is reported in the ExecResult), and
	// the ExecResult still carries whatever output arrived before the connection
	// broke.
	PodRunNoRetry(ctx context.Context, namespace, pod, container string, cmd []string, displayCmd string) (ExecResult, error)
}

// runner returns the node runner in use: the stub injected by a unit test, or
// the pod-exec implementation.
func (f *Framework) runner() nodeRunner {
	if f.nodeRun != nil {
		return f.nodeRun
	}
	return podRunner{f: f}
}

// podRunner implements nodeRunner on top of Kubernetes pod exec.
type podRunner struct {
	f *Framework
}

func (r podRunner) HostRun(ctx context.Context, nodeName string, cmd []string, displayCmd string) (ExecResult, error) {
	hostCmd, err := r.f.hostCmd(ctx, nodeName, cmd)
	if err != nil {
		return ExecResult{}, err
	}
	return r.f.execOnNode(ctx, sncTarget, nodeName, hostCmd, displayCmd)
}

func (r podRunner) HostRunNoRetry(ctx context.Context, nodeName string, cmd []string, displayCmd string) (ExecResult, error) {
	hostCmd, err := r.f.hostCmd(ctx, nodeName, cmd)
	if err != nil {
		return ExecResult{}, err
	}
	return r.f.execOnNodeNoRetry(ctx, sncTarget, nodeName, hostCmd, displayCmd)
}

func (r podRunner) DrbdsetupRun(ctx context.Context, nodeName string, args ...string) (ExecResult, error) {
	return r.f.Drbdsetup(ctx, nodeName, args...)
}

func (r podRunner) PodRun(
	ctx context.Context,
	namespace, pod, container string,
	cmd []string,
	displayCmd string,
) (ExecResult, error) {
	return r.f.execInPod(ctx, namespace, pod, container, cmd, displayCmd, true)
}

func (r podRunner) PodRunNoRetry(
	ctx context.Context,
	namespace, pod, container string,
	cmd []string,
	displayCmd string,
) (ExecResult, error) {
	return r.f.execInPod(ctx, namespace, pod, container, cmd, displayCmd, false)
}

// Drbdsetup executes `drbdsetup <args>` inside the agent pod running on
// nodeName and returns the result. Transport errors are returned as err;
// non-zero exit codes are reflected in ExecResult.ExitCode (not as errors).
// Goroutine-safe.
func (f *Framework) Drbdsetup(ctx context.Context, nodeName string, args ...string) (ExecResult, error) {
	cmd := append([]string{"drbdsetup"}, args...)
	return f.execOnNode(ctx, agentTarget, nodeName, cmd, "drbdsetup "+strings.Join(args, " "))
}

// LVM executes `lvm <args>` on the host of nodeName via nsenter inside the
// sds-node-configurator pod and returns the result. Goroutine-safe.
func (f *Framework) LVM(ctx context.Context, nodeName string, args ...string) (ExecResult, error) {
	lvm, err := f.resolveLvmBin(ctx, nodeName)
	if err != nil {
		return ExecResult{}, err
	}
	cmd := append([]string{lvm}, args...)
	return f.runner().HostRun(ctx, nodeName, cmd, "lvm "+strings.Join(args, " "))
}

// hostCmd prefixes cmd with the nsenter invocation that moves it into the host
// namespaces of nodeName.
func (f *Framework) hostCmd(ctx context.Context, nodeName string, cmd []string) ([]string, error) {
	nsenter, err := f.resolveNsenterBin(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	hostCmd := []string{nsenter, "-t", "1", "-m", "-u", "-i", "-n", "-p", "--"}
	return append(hostCmd, cmd...), nil
}

// resolveNsenterBin returns the nsenter binary path present in the
// sds-node-configurator pod on nodeName, caching the result. It probes
// nsenterCandidates in order — a binary that runs (any exit code) is treated
// as present, while a transport error means it is absent — so the suite
// tolerates node-configurator builds with or without the ".static" suffix.
// Goroutine-safe (uses podCacheMu).
func (f *Framework) resolveNsenterBin(ctx context.Context, nodeName string) (string, error) {
	f.podCacheMu.Lock()
	cached := f.nsenterBinResolved
	f.podCacheMu.Unlock()
	if cached != "" {
		return cached, nil
	}

	var lastErr error
	for _, cand := range nsenterCandidates {
		_, err := f.execOnNode(ctx, sncTarget, nodeName, []string{cand, "--version"}, "probe "+cand)
		if err == nil {
			f.podCacheMu.Lock()
			f.nsenterBinResolved = cand
			f.podCacheMu.Unlock()
			return cand, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("no nsenter binary found in %s pod on node %q (tried %v): %w",
		sncTarget.container, nodeName, nsenterCandidates, lastErr)
}

// resolveLvmBin returns the lvm binary path present on the HOST filesystem of
// nodeName, caching the result. Unlike nsenter, lvm is NOT shipped inside the
// sds-node-configurator pod — only nsenter is. lvm lives on the host at
// /opt/deckhouse/sds/bin/, so probing must go through nsenter (HostRun) into
// the host mount namespace, not via a direct exec in the pod (execOnNode).
//
// It probes lvmCandidates in order — a binary that runs to a zero exit is
// treated as present; a non-zero exit (nsenter: no such file or directory)
// means it is absent — so the suite tolerates node-configurator builds with
// or without the ".static" suffix. Goroutine-safe (uses podCacheMu).
func (f *Framework) resolveLvmBin(ctx context.Context, nodeName string) (string, error) {
	f.podCacheMu.Lock()
	cached := f.lvmBinResolved
	f.podCacheMu.Unlock()
	if cached != "" {
		return cached, nil
	}

	var lastErr error
	for _, cand := range lvmCandidates {
		// Re-check the cache before each probe: a concurrent goroutine may
		// have already resolved and cached a winner while we were waiting on
		// a prior probe. Probing further candidates after one has been
		// published is wasted work at best, and fatal at worst — a candidate
		// absent on this node returns an error that masks the cached success
		// and makes the whole helper fail with "no lvm binary found" even
		// though the cache already holds one.
		f.podCacheMu.Lock()
		cached := f.lvmBinResolved
		f.podCacheMu.Unlock()
		if cached != "" {
			return cached, nil
		}

		// Probe on the HOST through nsenter — lvm lives on the host filesystem,
		// not in the sds-node-configurator pod (only nsenter is). HostRun
		// prepends `nsenter -t 1 -m -u -i -n -p --` so the candidate path is
		// resolved against the host's mount table. A missing binary makes
		// nsenter exit non-zero (ExitError, err == nil, ExitCode != 0); a
		// transport error makes err != nil. Both mean "try the next candidate".
		res, err := f.runner().HostRun(ctx, nodeName, []string{cand, "version"}, "probe "+cand)
		if err == nil && res.ExitCode == 0 {
			f.podCacheMu.Lock()
			f.lvmBinResolved = cand
			f.podCacheMu.Unlock()
			return cand, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("probe %s on node %q: exit %d (stderr: %s)",
				cand, nodeName, res.ExitCode, res.Stderr)
		}
	}
	return "", fmt.Errorf("no lvm binary found on host of node %q (tried %v): %w",
		nodeName, lvmCandidates, lastErr)
}

// execOnNode discovers the pod matching target on nodeName, executes cmd
// inside it via SPDY, logs everything to GinkgoWriter, and returns the result.
// displayCmd is the human-readable command string used in log output.
//
// If the exec fails with a non-exit-code error and the pod name came from
// cache (stale entry), the cache entry is evicted, the pod is re-resolved,
// and the exec is retried once.
// Goroutine-safe.
func (f *Framework) execOnNode(ctx context.Context, target podTarget, nodeName string, cmd []string, displayCmd string) (ExecResult, error) {
	podName, cached, err := f.findPodOnNode(ctx, target, nodeName)
	if err != nil {
		return ExecResult{}, err
	}

	result, transportErr := f.doExec(ctx, target, podName, "node="+nodeName, cmd, displayCmd)
	if transportErr != nil && cached {
		fmt.Fprintf(GinkgoWriter, "[%s] [exec] node=%s $ %s -> transport error with cached pod %q, retrying with fresh lookup\n",
			time.Now().Format("15:04:05.000"), nodeName, displayCmd, podName)
		f.evictPodCache(target, nodeName)
		podName, _, err = f.findPodOnNode(ctx, target, nodeName)
		if err != nil {
			return ExecResult{}, err
		}
		result, transportErr = f.doExec(ctx, target, podName, "node="+nodeName, cmd, displayCmd)
	}
	if transportErr != nil {
		return result, fmt.Errorf("exec in pod %q on node %q (cmd: %s): %w\nstdout: %s\nstderr: %s",
			podName, nodeName, strings.Join(cmd, " "), transportErr, result.Stdout, result.Stderr)
	}

	return result, nil
}

// execOnNodeNoRetry is execOnNode without the retry: the command is executed
// at most once, so a command that must never run twice (a reboot, a spawn of a
// singleton process) cannot be duplicated by a transport error.
//
// A non-nil error is always a transport error — the caller cannot tell whether
// the command ran, and MUST decide from the partial output carried in the
// returned ExecResult.
func (f *Framework) execOnNodeNoRetry(ctx context.Context, target podTarget, nodeName string, cmd []string, displayCmd string) (ExecResult, error) {
	podName, _, err := f.findPodOnNode(ctx, target, nodeName)
	if err != nil {
		return ExecResult{}, err
	}

	result, transportErr := f.doExec(ctx, target, podName, "node="+nodeName, cmd, displayCmd)
	if transportErr != nil {
		return result, fmt.Errorf("exec in pod %q on node %q (cmd: %s): %w\nstdout: %s\nstderr: %s",
			podName, nodeName, strings.Join(cmd, " "), transportErr, result.Stdout, result.Stderr)
	}

	return result, nil
}

// execInPod executes cmd in container of the pod namespace/podName, the seam
// behind nodeRunner.PodRun and nodeRunner.PodRunNoRetry. The pod is addressed by
// name, so no discovery and no cache are involved; retry re-runs the command once
// on a transport error and MUST be false for anything that changes state in the
// pod.
func (f *Framework) execInPod(
	ctx context.Context,
	namespace, podName, container string,
	cmd []string,
	displayCmd string,
	retry bool,
) (ExecResult, error) {
	target := podTarget{namespace: namespace, container: container}
	where := "pod=" + namespace + "/" + podName

	result, transportErr := f.doExec(ctx, target, podName, where, cmd, displayCmd)
	if transportErr != nil && retry {
		fmt.Fprintf(GinkgoWriter, "[%s] [exec] %s $ %s -> transport error, retrying once\n",
			time.Now().Format("15:04:05.000"), where, displayCmd)
		result, transportErr = f.doExec(ctx, target, podName, where, cmd, displayCmd)
	}
	if transportErr != nil {
		return result, fmt.Errorf("exec in container %q of pod %s/%s (cmd: %s): %w\nstdout: %s\nstderr: %s",
			container, namespace, podName, strings.Join(cmd, " "), transportErr, result.Stdout, result.Stderr)
	}

	return result, nil
}

// doExec performs a single exec attempt against podName. where is the
// human-readable target the log lines and errors name it by ("node=worker-1"
// for the node channel, "pod=ns/name" for a direct pod exec). It returns the
// ExecResult and a non-nil transportErr when the failure is not an in-pod
// exit code (i.e. the pod may no longer exist).
func (f *Framework) doExec(ctx context.Context, target podTarget, podName, where string, cmd []string, displayCmd string) (ExecResult, error) {
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
		return ExecResult{}, fmt.Errorf("creating SPDY executor for pod %q (%s): %w", podName, where, err)
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [exec] %s $ %s\n",
		time.Now().Format("15:04:05.000"), where, displayCmd)

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

	var transportErr error
	if err != nil {
		if exitErr, ok := err.(utilexec.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			transportErr = err
		}
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [exec] %s $ %s -> exit=%d\n",
		time.Now().Format("15:04:05.000"), where, displayCmd, result.ExitCode)
	if combined.Len() > 0 {
		fmt.Fprint(GinkgoWriter, combined.String())
		if !strings.HasSuffix(combined.String(), "\n") {
			fmt.Fprintln(GinkgoWriter)
		}
	}

	return result, transportErr
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

// findPodOnNode returns the pod name matching target on nodeName.
// It caches the result so that subsequent calls skip the API request.
// The second return value indicates whether the result came from cache.
// Goroutine-safe (uses podCacheMu).
func (f *Framework) findPodOnNode(ctx context.Context, target podTarget, nodeName string) (string, bool, error) {
	key := podCacheKey{target: target, nodeName: nodeName}

	f.podCacheMu.Lock()
	if name, ok := f.podNameCache[key]; ok {
		f.podCacheMu.Unlock()
		return name, true, nil
	}
	f.podCacheMu.Unlock()

	pods, err := f.clientset.CoreV1().Pods(target.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: target.labelSelector,
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return "", false, fmt.Errorf("listing pods (label=%s) on node %q in namespace %s: %w",
			target.labelSelector, nodeName, target.namespace, err)
	}
	if len(pods.Items) != 1 {
		return "", false, fmt.Errorf("expected 1 pod (label=%s) on node %q in namespace %s, got %d",
			target.labelSelector, nodeName, target.namespace, len(pods.Items))
	}

	f.podCacheMu.Lock()
	f.podNameCache[key] = pods.Items[0].Name
	f.podCacheMu.Unlock()
	return pods.Items[0].Name, false, nil
}

// evictPodCache removes a cached pod name entry. Goroutine-safe.
func (f *Framework) evictPodCache(target podTarget, nodeName string) {
	f.podCacheMu.Lock()
	delete(f.podNameCache, podCacheKey{target: target, nodeName: nodeName})
	f.podCacheMu.Unlock()
}
