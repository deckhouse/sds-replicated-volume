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
	"context"
	"strings"
)

// Exec kinds recorded by stubRunner, so a test can tell a retrying exec from a
// no-retry one.
const (
	execKindHost        = "host"
	execKindHostNoRetry = "host-no-retry"
	execKindDrbdsetup   = "drbdsetup"
	execKindPod         = "pod"
	execKindPodNoRetry  = "pod-no-retry"
)

// execCall is one command the code under test asked a node or a pod to run.
type execCall struct {
	Kind    string
	Node    string
	Cmd     []string
	Display string

	// Namespace, Pod and Container are set by the pod kinds only, so a test can
	// assert which pod a helper addressed.
	Namespace string
	Pod       string
	Container string
}

// stubRunner is the nodeRunner used by helper unit tests: it records every
// command and answers from respond, so helpers can be exercised without a
// cluster and the number of executions can be asserted.
type stubRunner struct {
	calls   []execCall
	respond func(call execCall) (ExecResult, error)
}

func (s *stubRunner) HostRun(_ context.Context, node string, cmd []string, display string) (ExecResult, error) {
	return s.record(execKindHost, node, cmd, display)
}

func (s *stubRunner) HostRunNoRetry(_ context.Context, node string, cmd []string, display string) (ExecResult, error) {
	return s.record(execKindHostNoRetry, node, cmd, display)
}

func (s *stubRunner) DrbdsetupRun(_ context.Context, node string, args ...string) (ExecResult, error) {
	return s.record(execKindDrbdsetup, node, append([]string{"drbdsetup"}, args...), "drbdsetup "+strings.Join(args, " "))
}

func (s *stubRunner) PodRun(
	_ context.Context,
	namespace, pod, container string,
	cmd []string,
	display string,
) (ExecResult, error) {
	return s.recordCall(execCall{
		Kind: execKindPod, Namespace: namespace, Pod: pod, Container: container,
		Cmd: cmd, Display: display,
	})
}

func (s *stubRunner) PodRunNoRetry(
	_ context.Context,
	namespace, pod, container string,
	cmd []string,
	display string,
) (ExecResult, error) {
	return s.recordCall(execCall{
		Kind: execKindPodNoRetry, Namespace: namespace, Pod: pod, Container: container,
		Cmd: cmd, Display: display,
	})
}

func (s *stubRunner) record(kind, node string, cmd []string, display string) (ExecResult, error) {
	return s.recordCall(execCall{Kind: kind, Node: node, Cmd: cmd, Display: display})
}

func (s *stubRunner) recordCall(call execCall) (ExecResult, error) {
	s.calls = append(s.calls, call)
	if s.respond == nil {
		return ExecResult{}, nil
	}
	return s.respond(call)
}

// countKind counts recorded calls of one exec kind.
func (s *stubRunner) countKind(kind string) int {
	n := 0
	for i := range s.calls {
		if s.calls[i].Kind == kind {
			n++
		}
	}
	return n
}

// displays returns the display strings of all recorded calls, in order.
func (s *stubRunner) displays() []string {
	out := make([]string, len(s.calls))
	for i := range s.calls {
		out[i] = s.calls[i].Display
	}
	return out
}

// countCommandsContaining counts recorded calls whose command contains sub.
func (s *stubRunner) countCommandsContaining(sub string) int {
	n := 0
	for i := range s.calls {
		if strings.Contains(strings.Join(s.calls[i].Cmd, " "), sub) {
			n++
		}
	}
	return n
}

// countDisplaysWithPrefix counts recorded calls whose display starts with p.
func (s *stubRunner) countDisplaysWithPrefix(p string) int {
	n := 0
	for i := range s.calls {
		if strings.HasPrefix(s.calls[i].Display, p) {
			n++
		}
	}
	return n
}

// indexOfDisplayPrefix returns the position of the first call whose display
// starts with p, or -1.
func (s *stubRunner) indexOfDisplayPrefix(p string) int {
	for i := range s.calls {
		if strings.HasPrefix(s.calls[i].Display, p) {
			return i
		}
	}
	return -1
}
