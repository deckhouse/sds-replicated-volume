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

// PodLogWatcher observes pod log lines and fails the test on errors.
type PodLogWatcher interface {
	// SetupAllowErrors registers substrings so that error log lines containing
	// any of them are not reported. Cleanup removes the exclusion when the
	// scope ends.
	SetupAllowErrors(e envtesting.E, substrings ...string)

	// SetupLogLineWaiter returns a channel that receives the first log line
	// matching the given substring. The channel is closed after the match or
	// when the scope ends. Cleanup is registered on e.
	SetupLogLineWaiter(e envtesting.E, substring string) <-chan PodLogLine
}

// SetupErrorLogsWatcher reads from a PodLogLine channel, fails the test on
// error-level lines, and returns a PodLogWatcher for fine-grained control.
func SetupErrorLogsWatcher(e envtesting.E, ch <-chan PodLogLine) PodLogWatcher {
	w := &podLogWatcher{
		allowPatterns: make(map[string]int),
	}
	wg := sync.WaitGroup{}
	wg.Go(func() {
		for event := range ch {
			w.dispatch(event)
			if !isErrorLogLine(event.Line) {
				continue
			}
			if w.shouldIgnore(event.Line) {
				continue
			}
			e.Errorf("pod '%s': %s", event.PodName, event.Line)
		}
	})
	e.Cleanup(func() {
		wg.Wait()
	})
	return w
}

type podLogWatcher struct {
	mu            sync.RWMutex
	allowPatterns map[string]int
	waiters       []*logLineWaiter
}

type logLineWaiter struct {
	substring string
	ch        chan PodLogLine
	once      sync.Once
}

func (w *podLogWatcher) SetupAllowErrors(e envtesting.E, substrings ...string) {
	w.mu.Lock()
	for _, sub := range substrings {
		if sub != "" {
			w.allowPatterns[sub]++
		}
	}
	w.mu.Unlock()
	e.Cleanup(func() {
		w.mu.Lock()
		for _, sub := range substrings {
			if sub == "" {
				continue
			}
			if w.allowPatterns[sub] <= 1 {
				delete(w.allowPatterns, sub)
			} else {
				w.allowPatterns[sub]--
			}
		}
		w.mu.Unlock()
	})
}

func (w *podLogWatcher) SetupLogLineWaiter(e envtesting.E, substring string) <-chan PodLogLine {
	ch := make(chan PodLogLine, 1)
	wt := &logLineWaiter{substring: substring, ch: ch}
	w.mu.Lock()
	w.waiters = append(w.waiters, wt)
	w.mu.Unlock()
	e.Cleanup(func() {
		wt.once.Do(func() { close(ch) })
		w.mu.Lock()
		for i, cur := range w.waiters {
			if cur == wt {
				w.waiters = append(w.waiters[:i], w.waiters[i+1:]...)
				break
			}
		}
		w.mu.Unlock()
	})
	return ch
}

func (w *podLogWatcher) shouldIgnore(line string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for sub := range w.allowPatterns {
		if strings.Contains(line, sub) {
			return true
		}
	}
	return false
}

func (w *podLogWatcher) dispatch(event PodLogLine) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, wt := range w.waiters {
		if strings.Contains(event.Line, wt.substring) {
			wt.once.Do(func() {
				wt.ch <- event
				close(wt.ch)
			})
		}
	}
}

func isErrorLogLine(line string) bool {
	return strings.Contains(line, "error") ||
		strings.Contains(line, "ERROR") ||
		strings.Contains(line, "panic")
}
