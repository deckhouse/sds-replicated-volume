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
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// nodeAnswer is one scripted reply to a Node read: a snapshot, or a failure.
type nodeAnswer struct {
	node *corev1.Node
	err  error
}

// stubNodeReader answers Node reads from a script, so a poll loop can be walked
// snapshot by snapshot without a cluster. The last entry repeats forever, which
// is how a steady state is written as a single entry instead of a filled-in
// budget.
type stubNodeReader struct {
	script []nodeAnswer
	reads  int
}

func (s *stubNodeReader) getNode(_ context.Context, _ string) (*corev1.Node, error) {
	s.reads++
	answer := s.script[min(s.reads, len(s.script))-1]
	return answer.node, answer.err
}

// fakeClock drives a poll loop without sleeping: every wait advances a virtual
// now by exactly the interval the core asked for.
type fakeClock struct {
	now   time.Time
	waits int
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	c.waits++
	return nil
}

// bootedNode builds a Node reporting this boot ID and readiness.
func bootedNode(bootID string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	node.Status.NodeInfo.BootID = bootID
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	return node
}

var _ = Describe("RebootNode command", func() {
	It("prints the marker and syncs before rebooting, in one invocation", func() {
		cmd := rebootCommand()

		Expect(cmd[0]).To(Equal("sh"))
		script := cmd[2]
		Expect(script).To(ContainSubstring(rebootMarker))
		Expect(script).To(ContainSubstring("sync"))
		Expect(script).To(ContainSubstring("systemctl reboot"))
		Expect(strings.Index(script, rebootMarker)).To(BeNumerically("<", strings.Index(script, "systemctl reboot")),
			"the marker must be printed before the reboot, otherwise it proves nothing")
	})
})

var _ = Describe("RebootNode outcome classification", func() {
	transport := errors.New("error dialing backend: connection reset by peer")

	DescribeTable("classifies the single reboot exec",
		func(res ExecResult, execErr error, wantErr bool, wantMsg string) {
			stub := &stubRunner{respond: func(execCall) (ExecResult, error) { return res, execErr }}

			err := rebootNodeExec(context.Background(), stub, "worker-1")

			if wantErr {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(wantMsg))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			// The reboot must be issued exactly once in every outcome: the
			// classification alone cannot detect a retry, so count the execs.
			Expect(stub.calls).To(HaveLen(1))
			Expect(stub.calls[0].Kind).To(Equal(execKindHostNoRetry),
				"the reboot must bypass the retrying exec path")
			Expect(stub.countCommandsContaining("systemctl reboot")).To(Equal(1))
		},
		Entry("exit 0 with the marker: the reboot started",
			ExecResult{Stdout: rebootMarker + "\n"}, nil, false, ""),
		Entry("non-zero exit fails even with the marker",
			ExecResult{ExitCode: 1, Stdout: rebootMarker + "\n", Stderr: "Failed to reboot"}, nil,
			true, "exited with code 1"),
		Entry("non-zero exit without the marker fails",
			ExecResult{ExitCode: 127, Stderr: "systemctl: not found"}, nil,
			true, "exited with code 127"),
		Entry("transport error with the marker is expected: the node went down mid-exec",
			ExecResult{Stdout: rebootMarker + "\n"}, transport, false, ""),
		Entry("transport error without the marker fails: the reboot is unproven",
			ExecResult{}, transport, true, "failed before printing "+rebootMarker),
		Entry("exit 0 without the marker fails",
			ExecResult{Stdout: "\n"}, nil, true, "exited 0 without printing "+rebootMarker),
	)
})

var _ = Describe("RebootNode issue", func() {
	ctx := context.Background()

	// A runner that answers the preflight and reports a reboot that started.
	workingRunner := func() *stubRunner {
		return &stubRunner{respond: func(call execCall) (ExecResult, error) {
			if call.Kind == execKindHostNoRetry {
				return ExecResult{Stdout: rebootMarker + "\n"}, nil
			}
			return ExecResult{}, nil
		}}
	}

	It("records the boot ID, warms the exec path, then reboots exactly once", func() {
		reader := &stubNodeReader{script: []nodeAnswer{{node: bootedNode("boot-1", true)}}}
		runner := workingRunner()

		bootID, err := issueReboot(ctx, reader, runner, "worker-1")

		Expect(err).NotTo(HaveOccurred())
		Expect(bootID).To(Equal("boot-1"), "the completion poll watches this value for a change")
		Expect(runner.displays()).To(Equal([]string{"reboot preflight", "systemctl reboot"}),
			"the pod-name cache must be warm before the one command that must not run twice")
		Expect(runner.countCommandsContaining("systemctl reboot")).To(Equal(1))
	})

	It("touches nothing when the node cannot be read", func() {
		reader := &stubNodeReader{script: []nodeAnswer{{err: errors.New("connection refused")}}}
		runner := workingRunner()

		_, err := issueReboot(ctx, reader, runner, "worker-1")

		Expect(err).To(MatchError(ContainSubstring(`reading node "worker-1"`)))
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
		Expect(runner.calls).To(BeEmpty())
	})

	It("refuses a node that reports no boot ID, whose reboot could never be proven complete", func() {
		reader := &stubNodeReader{script: []nodeAnswer{{node: bootedNode("", true)}}}
		runner := workingRunner()

		_, err := issueReboot(ctx, reader, runner, "worker-1")

		Expect(err).To(MatchError(`node "worker-1" reports no boot ID`))
		Expect(runner.calls).To(BeEmpty())
	})

	It("stops at a failing preflight instead of issuing the reboot", func() {
		reader := &stubNodeReader{script: []nodeAnswer{{node: bootedNode("boot-1", true)}}}
		runner := &stubRunner{respond: func(call execCall) (ExecResult, error) {
			if call.Kind == execKindHost {
				return ExecResult{}, errors.New("no agent pod on the node")
			}
			return ExecResult{Stdout: rebootMarker + "\n"}, nil
		}}

		_, err := issueReboot(ctx, reader, runner, "worker-1")

		Expect(err).To(MatchError(ContainSubstring(`preflight exec on node "worker-1" failed`)))
		Expect(runner.countCommandsContaining("systemctl reboot")).To(BeZero())
	})

	It("names the node when the reboot is not proven to have started", func() {
		reader := &stubNodeReader{script: []nodeAnswer{{node: bootedNode("boot-1", true)}}}
		runner := &stubRunner{respond: func(call execCall) (ExecResult, error) {
			if call.Kind == execKindHostNoRetry {
				return ExecResult{}, errors.New("error dialing backend: connection reset by peer")
			}
			return ExecResult{}, nil
		}}

		_, err := issueReboot(ctx, reader, runner, "worker-1")

		Expect(err).To(MatchError(ContainSubstring(`node "worker-1"`)))
		Expect(err).To(MatchError(ContainSubstring("failed before printing " + rebootMarker)))
	})
})

var _ = Describe("RebootNode completion", func() {
	const (
		timeout = 30 * time.Second
		poll    = 5 * time.Second
	)

	// await runs the completion poll against a scripted node and a clock that
	// never really sleeps, and hands back the watch so the progress it recorded
	// can be asserted too.
	await := func(ctx context.Context, script []nodeAnswer) (*stubNodeReader, *fakeClock, *rebootWatch, error) {
		reader := &stubNodeReader{script: script}
		clock := &fakeClock{now: time.Unix(0, 0).UTC()}
		watch := &rebootWatch{nodeName: "worker-1", bootIDBefore: "boot-1", issuedAt: clock.Now()}
		err := awaitRebootCompleted(ctx, reader, clock, watch, timeout, poll)
		return reader, clock, watch, err
	}

	DescribeTable("judges the node against the completion criterion",
		func(script []nodeAnswer, wantReads int, wantErr ...string) {
			reader, _, _, err := await(context.Background(), script)

			if len(wantErr) == 0 {
				Expect(err).NotTo(HaveOccurred())
			} else {
				for _, want := range wantErr {
					Expect(err).To(MatchError(ContainSubstring(want)))
				}
			}
			Expect(reader.reads).To(Equal(wantReads))
		},
		Entry("an unchanged boot ID is not a completed reboot, however Ready the node looks",
			[]nodeAnswer{{node: bootedNode("boot-1", true)}}, 7,
			"did not come back from the reboot within 30s", `still reports boot ID "boot-1"`),
		Entry("a changed boot ID with NotReady is progress, not completion",
			[]nodeAnswer{{node: bootedNode("boot-2", false)}}, 7,
			`rebooted (boot ID "boot-2") but is not Ready yet`),
		Entry("a changed boot ID and Ready completes",
			[]nodeAnswer{
				{node: bootedNode("boot-1", true)},
				{node: bootedNode("boot-2", false)},
				{node: bootedNode("boot-2", true)},
			}, 3),
		Entry("a failed read is retried: the API server is reached through the outage",
			[]nodeAnswer{
				{err: errors.New("connection refused")},
				{node: bootedNode("boot-2", true)},
			}, 2),
		Entry("a read that never succeeds times out with the read error",
			[]nodeAnswer{{err: errors.New("connection refused")}}, 7,
			"did not come back from the reboot within 30s",
			`reading node "worker-1"`, "connection refused"),
	)

	It("records the first NotReady observation as progress", func() {
		_, _, watch, err := await(context.Background(), []nodeAnswer{
			{node: bootedNode("boot-1", false)},
			{node: bootedNode("boot-2", true)},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(watch.sawNotReady).To(BeTrue())
	})

	It("completes a reboot that was never published as NotReady", func() {
		_, _, watch, err := await(context.Background(), []nodeAnswer{{node: bootedNode("boot-2", true)}})

		Expect(err).NotTo(HaveOccurred())
		Expect(watch.sawNotReady).To(BeFalse(),
			"waiting for a NotReady that a fast reboot never publishes would be flaky")
	})

	It("gives the node the whole budget before giving up", func() {
		_, clock, _, err := await(context.Background(), []nodeAnswer{{node: bootedNode("boot-1", true)}})

		Expect(err).To(HaveOccurred())
		Expect(clock.waits).To(Equal(6))
		Expect(clock.now).To(Equal(time.Unix(0, 0).UTC().Add(timeout)))
	})

	It("stops as soon as the spec's context ends, reporting the last state", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		reader, clock, _, err := await(ctx, []nodeAnswer{{node: bootedNode("boot-1", true)}})

		Expect(err).To(MatchError(ContainSubstring(`waiting for node "worker-1" to come back`)))
		Expect(err).To(MatchError(context.Canceled))
		Expect(err).To(MatchError(ContainSubstring(`still reports boot ID "boot-1"`)))
		Expect(reader.reads).To(Equal(1))
		Expect(clock.waits).To(BeZero())
	})
})
