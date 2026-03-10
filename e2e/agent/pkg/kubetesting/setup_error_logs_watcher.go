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
	"strings"
	"sync"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// SetupErrorLogsWatcher reads from a PodLogLine channel and fails the test
// when an error-level log line is detected. The goroutine stops when the
// channel closes.
func SetupErrorLogsWatcher(e envtesting.E, ch <-chan PodLogLine) {
	wg := sync.WaitGroup{}
	wg.Go(func() {
		for event := range ch {
			if isErrorLogLine(event.Line) {
				e.Errorf("pod '%s': %s", event.PodName, event.Line)
			}
		}
	})
	e.Cleanup(func() {
		wg.Wait()
	})
}

// isErrorLogLine checks whether a log line indicates an error.
func isErrorLogLine(line string) bool {
	return strings.Contains(line, "error") ||
		strings.Contains(line, "ERROR") ||
		strings.Contains(line, "panic")
}
