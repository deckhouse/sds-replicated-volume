package suite

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupErrorLogsWatcher reads from a PodLogLine channel and fails the test
// when an error-level log line is detected. The goroutine stops when the
// channel closes.
func SetupErrorLogsWatcher(e *envtesting.E, ch <-chan PodLogLine) {
	go func() {
		for event := range ch {
			if isErrorLogLine(event.Line) {
				e.Errorf("pod '%s': %s", event.PodName, event.Line)
			}
		}
	}()
}

// isErrorLogLine checks whether a log line indicates an error.
func isErrorLogLine(line string) bool {
	return strings.Contains(line, "error") ||
		strings.Contains(line, "ERROR") ||
		strings.Contains(line, "panic")
}
