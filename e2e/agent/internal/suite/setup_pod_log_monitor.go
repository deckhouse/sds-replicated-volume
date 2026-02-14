package suite

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// PodLogMonitorOptions holds the config section for SetupPodLogMonitor.
type PodLogMonitorOptions struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"labelSelector"`
}

// SetupPodLogMonitor Provides agent pod log monitoring. It reads the
// "PodLogMonitorOptions" config section. All agent pods matching the configured
// label selector are streamed for logs. Error-level log lines are collected.
// During cleanup, any collected errors are reported as test failures.
func SetupPodLogMonitor(e *etesting.E) {
	// Require: read and validate config.
	var opts PodLogMonitorOptions
	e.Options(&opts)

	if opts.Namespace == "" {
		e.Fatal("require: namespace must not be empty")
	}
	if opts.LabelSelector == "" {
		e.Fatal("require: labelSelector must not be empty")
	}

	// Initialize kubernetes clientset for log streaming.
	kubeConfig, err := config.GetConfig()
	if err != nil {
		e.Fatalf("getting kubeconfig for pod log monitor: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		e.Fatalf("creating clientset for pod log monitor: %v", err)
	}

	// Discover agent pods.
	podList, err := clientset.CoreV1().Pods(opts.Namespace).List(e.Context(), metav1.ListOptions{
		LabelSelector: opts.LabelSelector,
	})
	if err != nil {
		e.Fatalf("listing agent pods: %v", err)
	}
	if len(podList.Items) == 0 {
		e.Fatalf("no agent pods found with selector %q in namespace %q",
			opts.LabelSelector, opts.Namespace)
	}

	// Arrange: start log streaming for each pod.
	var (
		mu         sync.Mutex
		errorLines []string
	)

	ctx, cancel := context.WithCancel(e.Context())

	for i := range podList.Items {
		pod := &podList.Items[i]
		podName := pod.Name

		sinceSeconds := int64(1)
		stream, err := clientset.CoreV1().Pods(opts.Namespace).GetLogs(podName, &corev1.PodLogOptions{
			Follow:       true,
			SinceSeconds: &sinceSeconds,
		}).Stream(ctx)
		if err != nil {
			e.Fatalf("streaming logs for pod %s: %v", podName, err)
		}

		go scanPodLogs(ctx, stream, podName, &mu, &errorLines)
	}

	// Cleanup: stop streaming and report errors.
	e.Cleanup(func() {
		cancel()

		mu.Lock()
		defer mu.Unlock()

		for _, line := range errorLines {
			e.Error(line)
		}
	})
}

// scanPodLogs reads log lines from a pod's log stream and collects error lines.
func scanPodLogs(
	ctx context.Context,
	stream io.ReadCloser,
	podName string,
	mu *sync.Mutex,
	errorLines *[]string,
) {
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		if isErrorLogLine(line) {
			mu.Lock()
			*errorLines = append(*errorLines, fmt.Sprintf("agent pod %s: %s", podName, line))
			mu.Unlock()
		}
	}
}

// isErrorLogLine checks whether a log line indicates an error.
// Supports both JSON structured logs ("level":"error") and text logs (level=error).
func isErrorLogLine(line string) bool {
	return strings.Contains(line, `"level":"error"`) ||
		strings.Contains(line, `"level":"ERROR"`) ||
		strings.Contains(line, "level=error") ||
		strings.Contains(line, "level=ERROR")
}
