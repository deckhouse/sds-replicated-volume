package suite

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/etesting"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/utils"
)

// SetupErrorLogsWatcher subscribes to a PodLogLine event source and fails the
// test as soon as an error-level log line is detected.
func SetupErrorLogsWatcher(e *etesting.E, source utils.EventSource[PodLogLine]) {
	cleanup := source.SetupSubscriber(func(event PodLogLine) {
		if isErrorLogLine(event.Value) {
			e.Errorf("pod '%s': %s", event.Key, event.Value)
		}
	})

	e.Cleanup(cleanup)
}

// isErrorLogLine checks whether a log line indicates an error.
func isErrorLogLine(line string) bool {
	return strings.Contains(line, "error") ||
		strings.Contains(line, "ERROR") ||
		strings.Contains(line, "panic")
}
