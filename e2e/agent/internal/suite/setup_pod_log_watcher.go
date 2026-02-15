package suite

import (
	"bufio"
	"context"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodLogMonitorOptions holds the config section for pod log monitoring.
type PodLogMonitorOptions struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
}

// PodLogLine is a log line tagged with the pod name it came from.
// Key is the pod name, Value is the log line.
type PodLogLine = utils.KeyedEvent[string, string]

// SetupPodLogWatcher discovers a clientset, starts streaming logs from a single
// pod, and returns an EventSource[string]. The stream is stopped during cleanup.
func SetupPodLogWatcher(
	e *etesting.E,
	namespace string,
	podName string,
) utils.EventSource[string] {
	clientset := DiscoverClientset(e)
	dispatcher := utils.NewEventDispatcher[string](nil)

	ctx, cancel := context.WithCancel(e.Context())
	wg := &sync.WaitGroup{}

	now := metav1.Now()
	stream, err := clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		SinceTime: &now,
	}).Stream(ctx)
	if err != nil {
		cancel()
		e.Fatalf("streaming logs for pod %s: %v", podName, err)
	}

	wg.Go(func() {
		defer stream.Close()

		scanner := bufio.NewScanner(stream)
		for ctx.Err() == nil && scanner.Scan() {
			dispatcher.DispatchEvent(scanner.Text())
		}
	})

	e.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	return dispatcher
}

// SetupPodsLogWatcher reads "PodLogMonitorOptions" config, discovers pods
// matching the label selector, starts a log watcher for each pod, and returns a
// single EventSource[PodLogLine] that fans in events from all watchers keyed by
// pod name.
func SetupPodsLogWatcher(e *etesting.E) utils.EventSource[PodLogLine] {
	var opts PodLogMonitorOptions
	e.Options(&opts)

	cl := DiscoverClient(e)

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

	sources := make(map[string]utils.EventSource[string], len(podList.Items))
	for i := range podList.Items {
		podName := podList.Items[i].Name
		sources[podName] = SetupPodLogWatcher(e, opts.Namespace, podName)
	}

	return utils.NewMultiEventSource(sources)
}
