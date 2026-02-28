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

package kubetesting

import (
	"bufio"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodLogMonitorOptions holds the config section for pod log monitoring.
type PodLogMonitorOptions struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
}

// PodLogLine is a log line tagged with the pod name it came from.
type PodLogLine struct {
	PodName string
	Line    string
}

// SetupPodLogWatcher starts streaming logs from a single pod and returns a
// channel of log lines. The channel is closed when the stream ends. The stream
// is stopped during cleanup.
func SetupPodLogWatcher(
	e envtesting.E,
	cs *kubernetes.Clientset,
	namespace string,
	podName string,
) <-chan string {
	ch := make(chan string)

	now := metav1.Now()
	stream, err := cs.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		SinceTime: &now,
	}).Stream(e.Context())
	if err != nil {
		e.Fatalf("streaming logs for pod %s: %v", podName, err)
	}

	wg := sync.WaitGroup{}
	wg.Go(func() {
		defer close(ch)

		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			ch <- scanner.Text()
		}
	})

	e.Cleanup(func() {
		stream.Close()
		wg.Wait()
	})

	return ch
}

// SetupPodsLogWatcher discovers pods matching opts.LabelSelector in
// opts.Namespace, starts a log watcher for each pod, and returns a single
// channel of PodLogLine that fans in log lines from all pods.
func SetupPodsLogWatcher(
	e envtesting.E,
	cl client.Client,
	cs *kubernetes.Clientset,
	opts PodLogMonitorOptions,
) <-chan PodLogLine {
	selector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		e.Fatalf("parsing label selector %q: %v", opts.LabelSelector, err)
	}

	podList := &corev1.PodList{}
	if err := cl.List(e.Context(), podList,
		client.InNamespace(opts.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		e.Fatalf("listing pods: %v", err)
	}
	if len(podList.Items) == 0 {
		e.Fatalf("no pods found with selector %q in namespace %q",
			opts.LabelSelector, opts.Namespace)
	}

	out := make(chan PodLogLine)
	var fanWg sync.WaitGroup

	for i := range podList.Items {
		podName := podList.Items[i].Name
		ch := SetupPodLogWatcher(e, cs, opts.Namespace, podName)
		fanWg.Go(func() {
			for line := range ch {
				out <- PodLogLine{PodName: podName, Line: line}
			}
		})
	}

	go func() {
		fanWg.Wait()
		close(out)
	}()

	return out
}
