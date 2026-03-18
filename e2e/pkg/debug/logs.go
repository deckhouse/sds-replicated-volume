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

package debug

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LogStreamer streams and filters pod logs for one or more components
// (e.g. "controller", "agent"). It uses controller-runtime cache for pod
// discovery and client-go GetLogs for log streaming.
type LogStreamer struct {
	clientset    kubernetes.Interface
	cache        cache.Cache
	emitter      Emitter
	formatter    Formatter
	filter       *Filter
	namespace    string
	snapshotsDir string
	kindReg      *KindRegistry
	trackers     map[string]*ReconcileIDTracker

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewLogStreamer creates a LogStreamer. Call Start to begin streaming.
func NewLogStreamer(
	clientset kubernetes.Interface,
	c cache.Cache,
	emitter Emitter,
	filter *Filter,
	ns string,
	snapshotsDir string,
	formatter Formatter,
	kr *KindRegistry,
) *LogStreamer {
	return &LogStreamer{
		clientset:    clientset,
		cache:        c,
		emitter:      emitter,
		formatter:    formatter,
		filter:       filter,
		namespace:    ns,
		snapshotsDir: snapshotsDir,
		kindReg:      kr,
		trackers:     make(map[string]*ReconcileIDTracker),
	}
}

// Component defines a log source: a component name and its pod label selector.
type Component struct {
	Name          string // e.g. "controller", "agent"
	LabelSelector string // e.g. "app=controller"
}

// Start begins streaming logs for the given components. Each component's
// pods are discovered via the shared cache informer, and logs are streamed
// using client-go GetLogs. Start is non-blocking.
func (ls *LogStreamer) Start(ctx context.Context, components ...Component) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ctx, ls.cancel = context.WithCancel(ctx)
	for _, comp := range components {
		tracker := NewReconcileIDTracker()
		ls.trackers[comp.Name] = tracker
		ls.wg.Add(1)
		go ls.followPodLogs(ctx, comp, tracker)
	}
}

// Stop cancels all streaming goroutines and waits for them to finish.
func (ls *LogStreamer) Stop() {
	ls.mu.Lock()
	cancel := ls.cancel
	ls.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	ls.wg.Wait()
}

// podTrackingState holds mutable per-component state for pod discovery.
type podTrackingState struct {
	mu              sync.Mutex
	activePods      map[string]context.CancelFunc
	notifiedWaiting bool
	wg              sync.WaitGroup
}

// podFromObj extracts a *corev1.Pod from an informer event object,
// handling both direct pods and DeletedFinalStateUnknown tombstones.
func podFromObj(obj interface{}) *corev1.Pod {
	if pod, ok := obj.(*corev1.Pod); ok {
		return pod
	}
	if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		if pod, ok := tombstone.Obj.(*corev1.Pod); ok {
			return pod
		}
	}
	return nil
}

// handlePodEvent processes a single pod add/update/delete event, starting
// or stopping per-pod log streamers as needed.
func (ls *LogStreamer) handlePodEvent(
	ctx context.Context,
	podName, eventType, compName string,
	tracker *ReconcileIDTracker,
	state *podTrackingState,
) {
	state.mu.Lock()
	defer state.mu.Unlock()

	switch eventType {
	case "ADDED", "MODIFIED":
		if _, exists := state.activePods[podName]; exists {
			return
		}
		podCtx, podCancel := context.WithCancel(ctx)
		state.activePods[podName] = podCancel
		state.notifiedWaiting = false
		state.wg.Add(1)
		go ls.streamPodLogs(podCtx, compName, podName, tracker, &state.wg)

	case "DELETED":
		if cancel, exists := state.activePods[podName]; exists {
			cancel()
			delete(state.activePods, podName)
		}
		if len(state.activePods) == 0 && !state.notifiedWaiting {
			ls.emitter.Emit(fmt.Sprintf("%s %s[%s]%s %s── no pods, waiting ──%s",
				Ts(), BadgeColor(compName), compName, ColorReset, ColorDim, ColorReset))
			state.notifiedWaiting = true
		}
	}
}

// followPodLogs discovers pods matching the component's label selector via
// the shared cache informer, then starts and stops per-pod log streamers
// as pods appear and disappear.
func (ls *LogStreamer) followPodLogs(ctx context.Context, comp Component, tracker *ReconcileIDTracker) {
	defer ls.wg.Done()

	informer, err := ls.cache.GetInformer(ctx, &corev1.Pod{})
	if err != nil {
		ls.emitter.Emit(fmt.Sprintf("%s %s[%s]%s failed to get pod informer: %v",
			Ts(), BadgeColor(comp.Name), comp.Name, ColorReset, err))
		return
	}

	selector, err := labels.Parse(comp.LabelSelector)
	if err != nil {
		ls.emitter.Emit(fmt.Sprintf("%s %s[%s]%s invalid label selector %q: %v",
			Ts(), BadgeColor(comp.Name), comp.Name, ColorReset, comp.LabelSelector, err))
		return
	}

	state := &podTrackingState{activePods: make(map[string]context.CancelFunc)}

	matchingPod := func(obj interface{}) *corev1.Pod {
		pod := podFromObj(obj)
		if pod == nil || pod.Namespace != ls.namespace || !selector.Matches(labels.Set(pod.Labels)) {
			return nil
		}
		return pod
	}

	reg, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if pod := matchingPod(obj); pod != nil {
				ls.handlePodEvent(ctx, pod.Name, "ADDED", comp.Name, tracker, state)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			if pod := matchingPod(newObj); pod != nil {
				ls.handlePodEvent(ctx, pod.Name, "MODIFIED", comp.Name, tracker, state)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if pod := matchingPod(obj); pod != nil {
				ls.handlePodEvent(ctx, pod.Name, "DELETED", comp.Name, tracker, state)
			}
		},
	})
	if err != nil {
		ls.emitter.Emit(fmt.Sprintf("%s %s[%s]%s failed to add event handler: %v",
			Ts(), BadgeColor(comp.Name), comp.Name, ColorReset, err))
		return
	}

	<-ctx.Done()

	_ = informer.RemoveEventHandler(reg)

	state.mu.Lock()
	for name, cancel := range state.activePods {
		cancel()
		delete(state.activePods, name)
	}
	state.mu.Unlock()

	state.wg.Wait()
}

// streamPodLogs discovers containers from the pod spec and starts one
// streaming goroutine per container. For single-container pods (or when the
// cache lookup fails), it falls back to streaming without specifying a
// container name, equivalent to kubectl logs --all-containers.
func (ls *LogStreamer) streamPodLogs(
	ctx context.Context,
	component, podName string,
	tracker *ReconcileIDTracker,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	containers := ls.podContainerNames(ctx, podName)
	if len(containers) <= 1 {
		container := ""
		if len(containers) == 1 {
			container = containers[0]
		}
		ls.streamContainerLogs(ctx, component, podName, container, tracker)
		return
	}

	var containerWg sync.WaitGroup
	for _, cn := range containers {
		containerWg.Add(1)
		go func(name string) {
			defer containerWg.Done()
			ls.streamContainerLogs(ctx, component, podName, name, tracker)
		}(cn)
	}
	containerWg.Wait()
}

// podContainerNames returns the names of all containers (excluding init
// containers) in the given pod, as read from the shared cache.
func (ls *LogStreamer) podContainerNames(ctx context.Context, podName string) []string {
	pod := &corev1.Pod{}
	key := client.ObjectKey{Namespace: ls.namespace, Name: podName}
	if err := ls.cache.Get(ctx, key, pod); err != nil {
		return nil
	}
	names := make([]string, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		names = append(names, c.Name)
	}
	return names
}

// streamContainerLogs tails logs from a single container in a pod using
// client-go GetLogs. Reconnects with exponential backoff when the pod is
// not yet ready, and uses SinceSeconds to avoid missing logs across
// reconnects. Pass container="" to stream from the default (single)
// container.
func (ls *LogStreamer) streamContainerLogs(
	ctx context.Context,
	component, podName, container string,
	tracker *ReconcileIDTracker,
) {
	const (
		minBackoff            = 2 * time.Second
		maxBackoff            = 30 * time.Second
		initialSinceSeconds   = int64(1)
		reconnectSinceSeconds = int64(5)
	)
	backoff := minBackoff
	sinceSeconds := initialSinceSeconds

	for {
		if ctx.Err() != nil {
			return
		}

		opts := &corev1.PodLogOptions{
			Follow:       true,
			SinceSeconds: &sinceSeconds,
		}
		if container != "" {
			opts.Container = container
		}

		stream, err := ls.clientset.CoreV1().Pods(ls.namespace).GetLogs(podName, opts).Stream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			sleepCtx(ctx, backoff)
			backoff = clampDuration(backoff*2, maxBackoff)
			continue
		}

		gotLines := ls.processLogStream(ctx, stream, component, tracker)
		_ = stream.Close()

		if ctx.Err() != nil {
			return
		}

		if gotLines {
			backoff = minBackoff
			sinceSeconds = reconnectSinceSeconds
		} else {
			backoff = clampDuration(backoff*2, maxBackoff)
		}
		sleepCtx(ctx, backoff)
	}
}

// processLogStream reads lines from a log stream, parses, filters, and
// emits them. Returns true if any lines were successfully read.
func (ls *LogStreamer) processLogStream(
	ctx context.Context,
	r io.Reader,
	component string,
	tracker *ReconcileIDTracker,
) bool {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1*1024*1024), 1*1024*1024)

	gotLines := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}

		raw := scanner.Text()
		if raw == "" {
			continue
		}
		gotLines = true

		entry := ParseLogEntry(raw, ls.snapshotsDir, ls.kindReg)
		if entry == nil {
			ls.emitter.Emit(fmt.Sprintf("%s %s[%s]%s %s",
				Ts(), BadgeColor(component), component, ColorReset, raw))
			continue
		}

		if !ls.filter.FilterLogEntry(entry) {
			continue
		}

		formatted := ls.formatter.FormatLogEntry(entry, component, tracker)
		ls.emitter.EmitBlock(formatted)
	}
	return gotLines
}

// sleepCtx sleeps for the given duration or until the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// clampDuration returns a capped at max.
func clampDuration(a, limit time.Duration) time.Duration {
	if a > limit {
		return limit
	}
	return a
}
